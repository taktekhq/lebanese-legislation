package cite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/judge"
)

// A Source is what is read and how it is asked: law articles, or rulings.
// Each has its own prompt version, so answers to one are never mistaken for
// the other, and its own file.
type Source struct {
	Name   string // "articles" or "rulings"
	Prompt string // the prompt version recorded with each answer
	System string
	Format string // how the answer is written, after the items
	Part   func(Item) string
	// MaxBytes caps the items' part of one call (0: no cap): a long judgment
	// makes a call of its own rather than a huge batch. claude takes the prompt
	// as one argument, and Linux refuses one over 128 KB.
	MaxBytes int
	// Order puts the items most worth reading first (nil: as given).
	Order func([]Item)
	// Actions are the relations this source's answers may use (nil: every
	// action in the schema); the API backend holds the model to them.
	Actions []string
}

// Articles reads law articles that change or cite another text.
var Articles = Source{Name: "articles", Prompt: Prompt, System: system, Format: batchFormat, Part: Item.batchPart, Order: verbFirst, MaxBytes: maxPartBytes}

// Rulings reads court rulings for the legal texts they apply, cite or
// interpret.
var Rulings = Source{Name: "rulings", Prompt: RulingPrompt, System: rulingSystem, Format: rulingFormat, Part: Item.rulingPart, MaxBytes: maxPartBytes,
	Actions: []string{"applies", "cites", "interprets", "other"}}

// maxPartBytes: 90 KB of items, and the prompt stays under 128 KB.
const maxPartBytes = 90000

// SourceByName finds a source by the name the -source flag gives.
func SourceByName(name string) (Source, error) {
	switch name {
	case "", "articles":
		return Articles, nil
	case "rulings":
		return Rulings, nil
	}
	return Source{}, fmt.Errorf("cite: unknown source %q (articles or rulings)", name)
}

// prompt is the whole prompt of one call for the CLIs, which take it as one
// piece: a line that clears any session, the instructions, then user(items).
func (s Source) prompt(items []Item) string {
	return "Ignore all earlier messages in this conversation; answer only this one.\n\n" + s.System + "\n\n" + s.user(items)
}

// user is the part of a prompt that changes from call to call: the items
// and how to write the answer. The API backend sends s.System apart from it,
// so every request shares the same cached prefix.
func (s Source) user(items []Item) string {
	var p strings.Builder
	p.WriteString("The " + s.Name + ":\n\n")
	for _, it := range items {
		p.WriteString(s.Part(it))
	}
	p.WriteString(s.Format)
	return p.String()
}

// A Backend is a model that reads batches. Each worker opens its own Session.
type Backend interface {
	Name() string // the model as recorded in each answer ("agy:Gemini ...", "claude:sonnet")
	Open() Session
}

// A Session reads one batch at a time; Close releases what it holds.
type Session interface {
	Read(prompt string) (string, error)
	Close()
}

// Claude reads through the Claude Code CLI, on a subscription: a fresh `claude
// -p` per batch (see judge.Claude: no tools, no MCP servers).
type Claude struct {
	C   *judge.Claude
	ctx context.Context
	dir string
}

// NewClaude makes a Claude backend; it runs in an empty folder, removed by
// Cleanup.
func NewClaude(ctx context.Context, bin, remote, model string, timeout time.Duration) (*Claude, error) {
	if model == "" {
		model = "sonnet"
	}
	dir, err := os.MkdirTemp("", "cite-claude-")
	if err != nil {
		return nil, err
	}
	return &Claude{C: &judge.Claude{Bin: bin, Model: model, Dir: dir, Remote: remote, Timeout: timeout}, ctx: ctx, dir: dir}, nil
}

// Cleanup removes the folder claude ran in.
func (c *Claude) Cleanup() { os.RemoveAll(c.dir) }

func (c *Claude) Name() string  { return "claude:" + c.C.Model }
func (c *Claude) Open() Session { return claudeSession{c} }

type claudeSession struct{ c *Claude }

func (s claudeSession) Read(prompt string) (string, error) {
	var raw rawJSON
	if _, err := s.c.C.Raw(s.c.ctx, prompt, &raw); err != nil {
		return "", err
	}
	return string(raw), nil
}
func (s claudeSession) Close() {}

// rawJSON keeps the JSON object the model answered, as it came.
type rawJSON []byte

func (r *rawJSON) UnmarshalJSON(b []byte) error { *r = append((*r)[:0], b...); return nil }

// Rulings.

// RulingPrompt is the prompt version of the rulings source.
const RulingPrompt = "ruling-1"

// RulingKey names a ruling in the citations file.
func RulingKey(id int) string { return fmt.Sprintf("ruling/%d", id) }

// RulingID reads a ruling's id back from its key; 0 when it isn't one.
func RulingID(key string) int {
	var id int
	if _, err := fmt.Sscanf(key, "ruling/%d", &id); err != nil {
		return 0
	}
	return id
}

// rulingMaxText is the most of one judgment sent to the model, in characters.
// Half of the rulings are under 700 characters and nine in ten under 2,300;
// the longest is 69,000. What is past the limit is not read, and a quote is
// only valid within what was sent.
const rulingMaxText = 20000

const rulingSystem = `You read judgments of Lebanese courts, in Arabic, and list every legal text the judgment applies, cites or interprets: laws, decree-laws, decrees, ministerial decisions, the constitution, the codes. Legal texts only: not other judgments or decisions of courts ("القرار المطعون فيه", "قرار محكمة التمييز رقم ..."), not doctrine, not the parties' contracts.

Write each target exactly as the judgment cites it. Do not guess what is not written: leave number, date or year empty when the judgment doesn't give them. A text named only by its title ("قانون الموجبات والعقود", "قانون العقوبات", "قانون اصول المحاكمات المدنية") has its title in name and kind "law" (kind "unknown" if you can't tell). When it says "the said law" or "القانون المذكور", write the text that phrase points to if the judgment names it elsewhere; otherwise kind "unknown".

One entry per target article: "المواد 3 و4 و5" is three; "المواد من 3 الى 6" is four. A reference to a text as a whole has no article. Write the same article once, however many times the judgment repeats it.

action, the relation:
- applies: the court bases its decision on the provision ("عملا باحكام المادة", "وفقا للمادة", "استنادا الى");
- interprets: the court explains the provision's meaning, scope or conditions ("المقصود بالمادة", "تفسير");
- cites: the provision is mentioned, quoted, or relied on by a party, without the court applying or interpreting it;
- other.

quote: copy, character for character, the shortest run of the judgment's own words that names the target, for example "عملا باحكام المادة 73 من القانون رقم 328 تاريخ 2 آب 2001". Never paraphrase it. Dates: YYYY-MM-DD. Article numbers in Western digits, with "مكرر" kept: "12 مكرر".`

const rulingFormat = `Answer for every key above. Output format: a single JSON object {"items":[{"key":"<the key>","amends":false,"changes":[{"action":"...","kind":"...","number":"","date":"","year":0,"name":"","self":false,"article":"","part":"","quote":"","why":""}]}]}. action is one of: applies, cites, interprets, other. kind is one of: law, decree-law, decree, decision, law-by-decree, constitution, unknown. Do not use any tools. Print only the JSON.`

func (it Item) rulingPart() string {
	var b strings.Builder
	fmt.Fprintf(&b, "### key: %s\nThe judgment: %s\nIts heading: %s\nThe judgment to read:\n%s\n\n", it.Key, it.Identity, it.Title, it.Text)
	return b.String()
}

// RulingItems selects the rulings with a text of some length (over 500
// characters: 29,311 of 47,615 today), in a fixed order by hash of the id, so
// "test" is the same 200 every time and a later "all" reads on from them.
// "short" selects the rest that have any text (summaries of 1 to 500
// characters: 16,725 today), which often still name the article applied.
func RulingItems(ctx context.Context, db *pgxpool.Pool, sample string) ([]Item, error) {
	q := `SELECT id, coalesce(court, ''), coalesce(number, ''), coalesce(year::text, ''),
			coalesce(to_char(decided_on, 'DD/MM/YYYY'), ''), coalesce(title, ''), text_ar
		FROM ruling WHERE `
	switch sample {
	case "", "all":
		q += `length(text_ar) > 500 ORDER BY md5(id::text)`
	case "test":
		q += `length(text_ar) > 500 ORDER BY md5(id::text) LIMIT 200`
	case "short":
		q += `length(btrim(text_ar)) BETWEEN 1 AND 500 ORDER BY md5(id::text)`
	default:
		return nil, fmt.Errorf("cite: unknown sample %q", sample)
	}
	rows, err := db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var court, number, year, date string
		if err := rows.Scan(&it.LawID, &court, &number, &year, &date, &it.Title, &it.Text); err != nil {
			return nil, err
		}
		if r := []rune(it.Text); len(r) > rulingMaxText {
			it.Text = string(r[:rulingMaxText])
		}
		it.Identity = strings.TrimSpace(court + " رقم " + number + "/" + year + " تاريخ " + date)
		it.Key = RulingKey(it.LawID)
		h := sha256.Sum256([]byte(it.Text + "\x00" + it.Identity + "\x00" + it.Title))
		it.Input = hex.EncodeToString(h[:8])
		out = append(out, it)
	}
	return out, rows.Err()
}

// firstJSON is the first {...} in an answer, for models that add a word or a
// code fence around it.
var firstJSON = regexp.MustCompile(`(?s)\{.*\}`)
