// Package cite asks a model (Gemini) to read an amending article and write
// down, in one fixed form, every text and article it changes: the kind,
// number, date and article as the amendment itself cites them. It never
// picks a record from our database and never writes the law's wording: the
// build resolves each citation against the data every night, so a target
// missing from our data today links itself once the data has it, and the
// citations nothing matches are a list of what our data lacks.
//
// Answers are kept as data (db/model/citations.jsonl), one line per amending
// article, with the prompt version, the model and a hash of what the model
// was shown. Every change carries the words of the article that name its
// target; an answer whose words aren't in the article is kept but not used.
package cite

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/amend"
	"github.com/taktekhq/lebanese-legislation/internal/judge"
)

// Prompt is the prompt version; answers made with another are asked again.
const Prompt = "cite-2"

// Target is a text and article as the amendment cites it.
type Target struct {
	Kind    string `json:"kind"`              // law, decree-law, decree, decision, law-by-decree, constitution, unknown
	Number  string `json:"number,omitempty"`  // as written: "328", "1274/2009"
	Date    string `json:"date,omitempty"`    // YYYY-MM-DD, when the amendment gives the full date
	Year    int    `json:"year,omitempty"`    // when it gives only the year
	Name    string `json:"name,omitempty"`    // a name it uses instead of or beside the number ("قانون العقوبات")
	Self    bool   `json:"self,omitempty"`    // the amending text itself ("هذا القانون")
	Article string `json:"article,omitempty"` // "73", "12 مكرر"; "" for the text as a whole
	Part    string `json:"part,omitempty"`    // the part named ("الفقرة الثانية"), "" for the whole article
}

// Changing says whether an action is a change the article itself makes
// (not a citation, nor the Centre's note of a change made elsewhere).
func Changing(action string) bool {
	switch action {
	case "replace", "amend", "repeal", "insert", "suspend", "extend", "general_repeal":
		return true
	}
	return false
}

// Change is one reference to one target.
type Change struct {
	// replace: the article (or part) is replaced or amended to read anew;
	// amend: words in it are edited (deleted, added, swapped);
	// repeal; insert: a new article or part is added; general_repeal: "all
	// provisions contrary to …" with no named target; suspend; other.
	Action string `json:"action"`
	Target Target `json:"target"`
	Quote  string `json:"quote"`         // the article's own words naming the target
	Why    string `json:"why,omitempty"` // one short sentence, English
	Valid  bool   `json:"valid"`         // Quote is in the article
}

// Citation is the model's reading of one amending article.
type Citation struct {
	Key     string   `json:"key"`   // the amending text's id and article: "329366/1"
	Input   string   `json:"input"` // hash of what the model was shown
	Prompt  string   `json:"prompt"`
	Model   string   `json:"model"`
	Amends  bool     `json:"amends"` // the article changes some legal text
	Changes []Change `json:"changes"`
	At      string   `json:"at"`
}

// Key names an amending article.
func Key(lawID int, article string) string { return fmt.Sprintf("%d/%s", lawID, article) }

// ID is a target's stable, ELI-style identifier, whether or not our data
// holds it: "lb/law/2001-08-02/328/art/73". Parts the citation doesn't give
// are left out, so two citations of the same article agree.
func (t Target) ID() string {
	p := []string{"lb", t.Kind}
	switch {
	case t.Self:
		p = append(p, "self")
	case t.Date != "":
		p = append(p, t.Date)
	case t.Year > 0:
		p = append(p, strconv.Itoa(t.Year))
	}
	if t.Number != "" {
		p = append(p, strings.ReplaceAll(amend.FoldString(t.Number), " ", ""))
	} else if t.Name != "" {
		p = append(p, strings.ReplaceAll(amend.FoldString(t.Name), " ", "-"))
	}
	if t.Article != "" {
		p = append(p, "art", strings.ReplaceAll(t.Article, " ", "-"))
	}
	return strings.Join(p, "/")
}

// Ref turns a target into the resolver's reference.
func (t Target) Ref() amend.Ref {
	r := amend.Ref{Self: t.Self, Year: t.Year}
	switch t.Kind {
	case amend.KindLaw, amend.KindDecreeLaw, amend.KindDecree, amend.KindDecision, amend.KindLawByDecree:
		r.Kind = t.Kind
	}
	if t.Number != "" {
		r.Number = amend.FoldString(t.Number)
		r.Num, _ = strconv.Atoi(leadingDigits(r.Number))
		if r.Year == 0 {
			if i := strings.Index(r.Number, "/"); i > 0 {
				if y, err := strconv.Atoi(strings.TrimSpace(r.Number[i+1:])); err == nil && y > 1900 && y < 2100 {
					r.Year = y
				}
			}
		}
	}
	if d, err := time.Parse("2006-01-02", t.Date); err == nil {
		r.Date = d
		r.Year = d.Year()
	}
	if t.Name != "" {
		// "قانون العقوبات اللبناني": the code's title has no "Lebanese".
		n := amend.FoldString(t.Name)
		if r.Date.IsZero() {
			if d := amend.FindDate(n); !d.IsZero() {
				r.Date, r.Year = d, d.Year()
			}
		}
		for _, s := range []string{" اللبناني", " اللبنانيه"} {
			n = strings.TrimSuffix(n, s)
		}
		r.Name = n
	}
	return r
}

func leadingDigits(s string) string {
	for i, c := range s {
		if c < '0' || c > '9' {
			return s[:i]
		}
	}
	return s
}

// Item is one amending article to read.
type Item struct {
	Key, Input string
	LawID      int
	Article    string
	Text       string // the article
	Identity   string // the amending text: kind, number, date
	Title      string
	Before     string // the articles before it in the same text, for "the said law"
}

// Items reads the amending articles: those whose wording has an amending
// verb, as the build's pattern pass selects them. sample "" takes all;
// "test" takes a fixed 200, half the pattern pass read and half it didn't.
func Items(ctx context.Context, db *pgxpool.Pool, sample string) ([]Item, error) {
	q := `WITH cand AS (
		SELECT a.law_id, a.number, a.position, a.text_ar,
			EXISTS (SELECT 1 FROM amendment m WHERE m.by_law_id = a.law_id AND m.by_article = a.number) AS parsed
		FROM legislation_article a
		WHERE ar_norm(a.text_ar) ~ '(تعدل|يعدل|عدلت|تستبدل|يستبدل|تلغي|يلغي|تضاف|يضاف)'
		   OR ar_norm(a.text_ar) ~ '(القانون|المرسوم|القرار|الاشتراعي|قانون|مرسوم|قرار) (رقم|الصادر|المنفذ|الاشتراعي)|رقم [0-9]+ ?(تاريخ|/)|من (قانون|المرسوم|القانون) ')`
	switch sample {
	case "", "all":
		q += ` SELECT * FROM cand`
	case "test":
		q += ` , pick AS (
			(SELECT * FROM cand WHERE parsed ORDER BY md5(law_id || '/' || number) LIMIT 100)
			UNION ALL
			(SELECT * FROM cand WHERE NOT parsed ORDER BY md5(law_id || '/' || number) LIMIT 100))
			SELECT * FROM pick`
	default:
		return nil, fmt.Errorf("cite: unknown sample %q", sample)
	}
	q = `WITH sel AS (` + q + `)
		SELECT s.law_id, s.number, s.text_ar,
			l.kind || coalesce(' رقم ' || l.number, '') || coalesce(' تاريخ ' || to_char(l.issued_on, 'DD/MM/YYYY'), ''),
			coalesce(l.title, ''),
			coalesce((SELECT string_agg(left(p.text_ar, 1500), E'\n---\n' ORDER BY p.position)
				FROM (SELECT text_ar, position FROM legislation_article b
					WHERE b.law_id = s.law_id AND b.position < s.position
					ORDER BY b.position DESC LIMIT 3) p), '')
		FROM sel s JOIN legislation l ON l.id = s.law_id
		ORDER BY s.law_id, s.position`
	rows, err := db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.LawID, &it.Article, &it.Text, &it.Identity, &it.Title, &it.Before); err != nil {
			return nil, err
		}
		if len([]rune(it.Text)) > 12000 {
			it.Text = string([]rune(it.Text)[:12000])
		}
		it.Key = Key(it.LawID, it.Article)
		h := sha256.Sum256([]byte(it.Text + "\x00" + it.Identity + "\x00" + it.Title + "\x00" + it.Before))
		it.Input = hex.EncodeToString(h[:8])
		out = append(out, it)
	}
	return out, rows.Err()
}

const system = `You read articles of Lebanese legislation, in Arabic, and list every reference the article makes to a legal text: changes it makes (amendments, repeals, additions), and other links (it cites, applies or departs from a text). Another text, or the one it belongs to.

Write each target exactly as the article cites it. Do not guess what is not written: leave number, date or year empty when the article doesn't give them. When it says "the said law", "the same decree", "القانون المذكور" or similar, write the text that phrase points to, using the articles before it and the title; if you can't tell, use kind "unknown". A reference to the article's own text ("هذا القانون", "المادة 5 من هذا المرسوم") has self true.

One entry per target article: "المواد 3 و4 و5" is three; "المواد من 3 الى 6" is four. A reference to a text as a whole has no article.

action, the relation:
- replace: the article (or a part) is replaced or amended to read anew; amend: words in it are edited (deleted, added, swapped); repeal; insert: a new article or part is added; suspend; extend: a deadline or validity is extended;
- general_repeal: "تلغى جميع الاحكام المخالفة" and similar, with no named text (kind unknown);
- cites: refers to the text ("وفقا لاحكام المادة 5 من القانون رقم ..."); applies: the article implements or is issued under the text ("تطبيقا لاحكام", "تنفيذا ل"); derogates: departs from it ("خلافا لاحكام", "استثناء من");
- note_amended_by, note_repealed_by, note_added_by: the Lebanese University Centre's own notes inside the article ("عدلت بموجب القانون رقم ...", "ألغيت بموجب ...", "أضيفت بموجب ..."): the target is the text named as having made the change;
- other.
amends: true when the article itself changes a legal text (replace, amend, repeal, insert, suspend, extend, general_repeal).

quote: copy, character for character, the shortest run of the article's own words that names the target, for example "تعدل المادة 73 من القانون رقم 328 تاريخ 2 آب 2001". Never paraphrase it. Dates: YYYY-MM-DD. Article numbers in Western digits, with "مكرر" kept: "12 مكرر".`

var schema = map[string]any{
	"type": "OBJECT",
	"properties": map[string]any{
		"amends": map[string]any{"type": "BOOLEAN"},
		"changes": map[string]any{"type": "ARRAY", "items": map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"action":  map[string]any{"type": "STRING", "enum": []string{"replace", "amend", "repeal", "insert", "suspend", "extend", "general_repeal", "cites", "applies", "derogates", "note_amended_by", "note_repealed_by", "note_added_by", "other"}},
				"kind":    map[string]any{"type": "STRING", "enum": []string{"law", "decree-law", "decree", "decision", "law-by-decree", "constitution", "unknown"}},
				"number":  map[string]any{"type": "STRING"},
				"date":    map[string]any{"type": "STRING", "description": "YYYY-MM-DD, or empty"},
				"year":    map[string]any{"type": "INTEGER", "description": "0 when not given"},
				"name":    map[string]any{"type": "STRING", "description": "the name the article uses for the text, if any"},
				"self":    map[string]any{"type": "BOOLEAN", "description": "the change is to this same text"},
				"article": map[string]any{"type": "STRING", "description": "empty for the text as a whole"},
				"part":    map[string]any{"type": "STRING", "description": "the paragraph, clause or words named; empty for the whole article"},
				"quote":   map[string]any{"type": "STRING"},
				"why":     map[string]any{"type": "STRING", "description": "one short sentence in English"},
			},
			"required": []string{"action", "kind", "quote"},
		}},
	},
	"required": []string{"amends", "changes"},
}

func (it Item) prompt() string {
	var b strings.Builder
	fmt.Fprintf(&b, "The text: %s\nIts title: %s\n\n", it.Identity, it.Title)
	if it.Before != "" {
		fmt.Fprintf(&b, "The articles before it, for context only:\n%s\n\n", it.Before)
	}
	fmt.Fprintf(&b, "The article to read (article %s):\n%s\n", it.Article, it.Text)
	return b.String()
}

type answer struct {
	Amends  bool `json:"amends"`
	Changes []struct {
		Action, Kind, Number, Date, Name, Article, Part, Quote, Why string
		Year                                                        int
		Self                                                        bool
	} `json:"changes"`
}

// check marks each change valid when its quote is the article's own words.
func check(text string, a answer) []Change {
	folded := strings.Join(strings.Fields(amend.FoldString(text)), " ")
	var out []Change
	for _, c := range a.Changes {
		q := strings.Join(strings.Fields(amend.FoldString(c.Quote)), " ")
		out = append(out, Change{
			Action: c.Action,
			Target: Target{Kind: c.Kind, Number: strings.TrimSpace(c.Number), Date: strings.TrimSpace(c.Date), Year: c.Year,
				Name: strings.TrimSpace(c.Name), Self: c.Self, Article: strings.TrimSpace(c.Article), Part: strings.TrimSpace(c.Part)},
			Quote: c.Quote,
			Why:   c.Why,
			Valid: q != "" && strings.Contains(folded, q),
		})
	}
	return out
}

// Stats counts a run.
type Stats struct {
	Asked, Skipped, Failed, Changes, Invalid int
	Calls, TextChars, PromptChars            int // calls made (retries count), characters of item text read, characters sent
	Usage                                    judge.Usage
	// Keep (redo "keep:...") merges a re-read with the earlier answer: the
	// earlier answer's citations whose quote is in the text stay beside the
	// new ones, so a re-read can only add usable citations.
	Keep bool
}

// Run asks the model about each item not yet read with this prompt and
// input, saving to path as it goes.
// redo, when set, asks again about items already read by a model whose name
// contains it (a cheaper first pass, read again by a better model).
func Run(ctx context.Context, g *judge.Gemini, items []Item, path string, workers, limit int, redo string, log func(string, ...any)) (Stats, error) {
	var st Stats
	have, err := Load(path)
	if err != nil {
		return st, err
	}
	var todo []Item
	for _, it := range items {
		if c, ok := have[it.Key]; ok && c.Input == it.Input && c.Prompt == Prompt &&
			(redo == "" || !strings.Contains(c.Model, redo)) {
			st.Skipped++
			continue
		}
		todo = append(todo, it)
	}
	if limit > 0 && len(todo) > limit {
		todo = todo[:limit]
	}
	log("%d to read, %d already read", len(todo), st.Skipped)
	var mu sync.Mutex
	jobs := make(chan Item)
	var wg sync.WaitGroup
	for w := 0; w < max(workers, 1); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range jobs {
				var a answer
				model, u, err := g.Ask(ctx, system, it.prompt(), schema, &a)
				mu.Lock()
				st.Usage.In += u.In
				st.Usage.Out += u.Out
				st.Usage.Thinking += u.Thinking
				if err != nil {
					st.Failed++
					log("%s: %v", it.Key, err)
					mu.Unlock()
					continue
				}
				c := Citation{Key: it.Key, Input: it.Input, Prompt: Prompt, Model: model, Amends: a.Amends,
					Changes: check(it.Text, a), At: time.Now().UTC().Format(time.RFC3339)}
				st.Asked++
				st.Changes += len(c.Changes)
				for _, ch := range c.Changes {
					if !ch.Valid {
						st.Invalid++
					}
				}
				have[it.Key] = c
				if st.Asked%10 == 0 {
					if err := Save(path, have); err != nil {
						log("save: %v", err)
					}
					log("%d read, %d changes (%d with a quote not in the article); tokens in %d out %d thinking %d",
						st.Asked, st.Changes, st.Invalid, st.Usage.In, st.Usage.Out, st.Usage.Thinking)
				}
				mu.Unlock()
			}
		}()
	}
	for _, it := range todo {
		select {
		case jobs <- it:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	return st, Save(path, have)
}

// Load reads a citations file, the last line for each key winning; a
// missing file is empty.
func Load(path string) (map[string]Citation, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]Citation{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Citation{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var c Citation
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, err
		}
		out[c.Key] = c
	}
	return out, sc.Err()
}

// Save writes citations sorted by key, one JSON object a line.
func Save(path string, cs map[string]Citation) error {
	keys := make([]string, 0, len(cs))
	for k := range cs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	for _, k := range keys {
		line, err := json.Marshal(cs[k])
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
