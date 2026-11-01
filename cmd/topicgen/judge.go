package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

// Statement is one claim of a page, in both languages, with the articles it
// cites. Key is where it sits: answer, s2p1 (section 2, paragraph 1), s3r4
// (section 3, table row 4).
type Statement struct {
	Key   string
	EN    string
	AR    string
	Cites []string
}

func statements(t *topics.Topic) []Statement {
	var out []Statement
	out = append(out, Statement{"answer", t.EN.Answer.Text, t.AR.Answer.Text, t.EN.Answer.Cites})
	for i, s := range t.EN.Sections {
		a := t.AR.Sections[i]
		for j, p := range s.Paras {
			if !p.Note {
				out = append(out, Statement{fmt.Sprintf("s%dp%d", i+1, j+1), p.Text, a.Paras[j].Text, p.Cites})
			}
		}
		if s.Table != nil {
			for k, r := range s.Table.Rows {
				out = append(out, Statement{fmt.Sprintf("s%dr%d", i+1, k+1), strings.Join(r.Cells, " | "), strings.Join(a.Table.Rows[k].Cells, " | "), r.Cites})
			}
		}
	}
	return out
}

// Verdict is the judge's reading of one statement.
type Verdict struct {
	Key     string `json:"key"`
	Verdict string `json:"verdict"` // supported, partly, unsupported, contradicted
	Issue   string `json:"issue"`
}

// judgePage asks a model to re-ground a page: it lists the facts the cited
// articles state, independently of the page, and only then compares every
// statement against that list.
func judgePage(ctx context.Context, db topics.DB, fam *family, t *topics.Topic) ([]string, []Verdict, error) {
	var b strings.Builder
	b.WriteString("You are checking a topic page on Lebanese law against the published articles it cites. The page may state only what those articles state.\n\nTHE ARTICLES CITED\n")
	seen := map[string]bool{}
	for _, c := range t.Articles {
		k := fmt.Sprintf("%d/%s", c.Law, c.Article)
		if seen[k] {
			continue
		}
		seen[k] = true
		f, _, err := db.Article(c.Law, c.Article)
		if err != nil {
			return nil, nil, err
		}
		fmt.Fprintf(&b, "\n=== law %d (%s), article %s ===\n%s\n", c.Law, lawIdent(ctx, db, c.Law), c.Article, f.Text)
	}
	b.WriteString("\nTHE STATEMENTS TO CHECK (English, then the Arabic version; the cited articles are in brackets)\n")
	for _, s := range statements(t) {
		fmt.Fprintf(&b, "\n[%s] cites %s\nEN: %s\nAR: %s\n", s.Key, citeList(t, s.Cites), s.EN, s.AR)
	}
	b.WriteString(`

TASK
Step 1. Without looking at the statements' wording, list from the articles alone the facts they state: "facts": ["F1: article N says ...", ...]. Include limits (what an article leaves to a later decree, what it repeals).
Step 2. Compare each statement, English and Arabic, with that list and the cited articles. Give each a verdict:
- "supported": every claim in it, in both languages, is stated by a cited article.
- "partly": it adds a detail, a condition, or a figure the cited articles do not state, or it overstates or leaves out a condition that changes the meaning.
- "unsupported": its main claim is not in the cited articles.
- "contradicted": a cited article says otherwise.
- the Arabic must say what the English says: a difference in meaning is "partly".
A law's name, number and date may be taken from the heading of its articles above: do not flag them. Be strict about figures, periods, conditions ("only if", "unless") and who the rule applies to. Plain restating in simpler words is fine.
Reply with one JSON object and nothing else:
{"facts": ["F1: ..."], "verdicts": [{"key": "answer", "verdict": "supported", "issue": ""}, {"key": "s1p1", "verdict": "partly", "issue": "<what is not in the article>"}]}`)
	out, err := callFamily(ctx, fam, b.String())
	if err != nil {
		return nil, nil, err
	}
	i, j := strings.Index(out, "{"), strings.LastIndex(out, "}")
	var r struct {
		Facts    []string  `json:"facts"`
		Verdicts []Verdict `json:"verdicts"`
	}
	if i < 0 || j < i || json.Unmarshal([]byte(out[i:j+1]), &r) != nil {
		return nil, nil, fmt.Errorf("unparseable verdict: %.200q", out)
	}
	return r.Facts, r.Verdicts, nil
}

func citeList(t *topics.Topic, ids []string) string {
	var r []string
	for _, id := range ids {
		if c := t.Cite(id); c != nil {
			r = append(r, fmt.Sprintf("law %d art. %s", c.Law, c.Article))
		}
	}
	return strings.Join(r, "; ")
}

// judgeTopics re-grounds the topic files with the family given by -family
// and writes what it flags to work/judge/<slug>.json.
func judgeTopics(ctx context.Context, db topics.DB, slugs []string) {
	fam := families[*famName]
	dir := filepath.Join(*workDir, "judge")
	os.MkdirAll(dir, 0o755)
	forEach(slugs, func(slug string) {
		t := topics.Get(slug)
		if t == nil {
			log.Printf("%s: no such topic", slug)
			return
		}
		facts, vs, err := judgePage(ctx, db, fam, t)
		if err != nil {
			log.Printf("%s: %v", slug, err)
			return
		}
		b, _ := json.MarshalIndent(map[string]any{"judge": fam.name, "facts": facts, "verdicts": vs}, "", "  ")
		os.WriteFile(filepath.Join(dir, slug+"."+fam.name+".json"), b, 0o644)
		bad := 0
		for _, v := range vs {
			if v.Verdict != "supported" {
				bad++
				log.Printf("%s [%s] %s: %s", slug, v.Key, v.Verdict, v.Issue)
			}
		}
		log.Printf("%s: %d statements checked, %d flagged", slug, len(vs), bad)
	})
}

func lawIdent(ctx context.Context, db topics.DB, id int) string {
	var title, num string
	var issued *time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT title, coalesce(number, ''), issued_on FROM legislation WHERE id = $1`, id).Scan(&title, &num, &issued); err != nil {
		return ""
	}
	s := title
	if num != "" {
		s += "; no. " + num
	}
	if issued != nil {
		s += ", dated " + issued.Format("2/1/2006")
	}
	return s
}

// polish judges the pages (-family judges), has the writer (-writer) correct
// what was flagged, checks, and repeats: -rounds times at most.
func polish(ctx context.Context, db topics.DB, slugs []string) {
	judge, writer := families[*famName], families[*writerName]
	dir := filepath.Join(*workDir, "polish")
	os.MkdirAll(dir, 0o755)
	for round := 1; round <= *rounds; round++ {
		var mu sync.Mutex
		flagged := 0
		forEach(slugs, func(slug string) {
			t := loadFile(slug)
			if t == nil {
				return
			}
			facts, vs, err := judgePage(ctx, db, judge, t)
			if err != nil {
				log.Printf("%s: %v", slug, err)
				return
			}
			b, _ := json.MarshalIndent(map[string]any{"round": round, "judge": judge.name, "facts": facts, "verdicts": vs}, "", "  ")
			os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s.round%d.json", slug, round)), b, 0o644)
			stm := map[string]Statement{}
			for _, s := range statements(t) {
				stm[s.Key] = s
			}
			var issues []string
			for _, v := range vs {
				if v.Verdict != "supported" {
					s := stm[v.Key]
					issues = append(issues, fmt.Sprintf("[%s] %s: %s\n  EN: %s", v.Key, v.Verdict, v.Issue, s.EN))
				}
			}
			log.Printf("round %d %s: %d statements, %d flagged", round, slug, len(vs), len(issues))
			if len(issues) == 0 {
				return
			}
			mu.Lock()
			flagged++
			mu.Unlock()
			nt, _, err := refine(ctx, db, writer, t, issues)
			if err != nil || nt == nil {
				log.Printf("%s: refine failed: %v", slug, err)
				return
			}
			if p := topics.Check(nt, db); len(p) > 0 {
				log.Printf("%s: refined page still has %d problems; kept the old page", slug, len(p))
				return
			}
			nt.Updated = *today
			if err := writeTopic(nt); err != nil {
				log.Printf("%s: %v", slug, err)
			}
		})
		log.Printf("round %d: %d pages flagged", round, flagged)
		if flagged == 0 {
			break
		}
	}
}

// loadFile reads a topic file from -out (not the embedded copy: polish rewrites them).
func loadFile(slug string) *topics.Topic {
	b, err := os.ReadFile(filepath.Join(*outDir, slug+".json"))
	if err != nil {
		log.Printf("%s: %v", slug, err)
		return nil
	}
	t := &topics.Topic{}
	if err := json.Unmarshal(b, t); err != nil {
		log.Printf("%s: %v", slug, err)
		return nil
	}
	return t
}
