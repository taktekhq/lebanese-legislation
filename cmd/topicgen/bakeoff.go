package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

// The blind bake-off: the same prompt, the same article texts, three model
// families. Each page is scored by the deterministic checks of the topics
// package, then read blind by all three families as judges, each judge's
// own family left out of the average.

var familyOrder = []string{"claude", "openai", "gemini"}

// Candidate is one family's page for one topic.
type Candidate struct {
	Slug, Family string
	Seconds      float64
	Err          string
	Raw          Stats // quotes as the model wrote them
	Problems     []string
	Structural   int // problems that are not about the database
	Grounding    int
	Uncited      int
	Dashes       int
	Words        int
	Topic        *topics.Topic `json:"-"`
}

func bakeoff(ctx context.Context, db topics.DB, slugs []string) {
	dir := filepath.Join(*workDir, "bakeoff")
	os.MkdirAll(dir, 0o755)
	var mu sync.Mutex
	cands := map[string]map[string]*Candidate{} // slug -> family -> candidate
	type job struct{ slug, fam string }
	var jobs []string
	for _, s := range slugs {
		cands[s] = map[string]*Candidate{}
		for _, f := range familyOrder {
			jobs = append(jobs, s+"|"+f)
		}
	}
	forEach(jobs, func(j string) {
		slug, fn := cut2(j)
		fam := families[fn]
		s, arts := mustSpec(ctx, db, slug)
		c := &Candidate{Slug: slug, Family: fn}
		start := time.Now()
		raw, err := callFamily(ctx, fam, buildPrompt(s, arts))
		c.Seconds = time.Since(start).Seconds()
		os.WriteFile(filepath.Join(dir, slug+"."+fn+".raw.txt"), []byte(raw), 0o644)
		if err != nil {
			c.Err = err.Error()
		} else if p, err := parseAnswer(raw); err != nil {
			c.Err = err.Error()
		} else {
			t, st := assemble(s, p, packTexts(arts), true)
			c.Raw, c.Topic = st, t
			// the em dashes the model wrote, counted before they were stripped
			c.Dashes = strings.Count(raw, "\u2014")
			score(c, db)
			b, _ := json.MarshalIndent(t, "", "  ")
			os.WriteFile(filepath.Join(dir, slug+"."+fn+".json"), b, 0o644)
		}
		mu.Lock()
		cands[slug][fn] = c
		mu.Unlock()
		log.Printf("%s %s: %.0fs, %d problems, err=%q", slug, fn, c.Seconds, len(c.Problems), c.Err)
	})
	// judges
	type verdict struct {
		Judge, Slug string
		Scores      map[string]map[string]any // label -> scores
		Facts       []string
		Labels      map[string]string // label -> family
		Err         string
	}
	var verdicts []verdict
	var jj []string
	for _, s := range slugs {
		for _, f := range familyOrder {
			jj = append(jj, s+"|"+f)
		}
	}
	forEach(jj, func(j string) {
		slug, jf := cut2(j)
		_, arts := mustSpec(ctx, db, slug)
		labels, prompt := judgePrompt(slug, arts, cands[slug])
		v := verdict{Judge: jf, Slug: slug, Labels: labels}
		out, err := callFamily(ctx, families[jf], prompt)
		os.WriteFile(filepath.Join(dir, slug+".judge-"+jf+".raw.txt"), []byte(out), 0o644)
		if err != nil {
			v.Err = err.Error()
		} else {
			var r struct {
				Facts  []string                  `json:"facts"`
				Scores map[string]map[string]any `json:"scores"`
			}
			i, k := strings.Index(out, "{"), strings.LastIndex(out, "}")
			if i < 0 || k < i || json.Unmarshal([]byte(out[i:k+1]), &r) != nil {
				v.Err = "unparseable verdict"
			} else {
				v.Scores, v.Facts = r.Scores, r.Facts
			}
		}
		mu.Lock()
		verdicts = append(verdicts, v)
		mu.Unlock()
		log.Printf("judge %s on %s: err=%q", jf, slug, v.Err)
	})
	report(dir, slugs, cands, func() any { return verdicts })
	// aggregate: mean score per family over other families' judges
	type agg struct{ sum, n map[string]float64 }
	crit := []string{"accuracy", "clarity", "completeness", "arabic"}
	sum := map[string]map[string]float64{}
	cnt := map[string]map[string]float64{}
	for _, f := range familyOrder {
		sum[f], cnt[f] = map[string]float64{}, map[string]float64{}
	}
	for _, v := range verdicts {
		for label, sc := range v.Scores {
			fam := v.Labels[label]
			if fam == "" || fam == v.Judge {
				continue
			}
			for _, c := range crit {
				if x, ok := sc[c].(float64); ok {
					sum[fam][c] += x
					cnt[fam][c]++
				}
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "| family | accuracy | clarity | completeness | arabic | mean |\n|---|---|---|---|---|---|\n")
	for _, f := range familyOrder {
		fmt.Fprintf(&b, "| %s", f)
		tot, n := 0.0, 0.0
		for _, c := range crit {
			m := 0.0
			if cnt[f][c] > 0 {
				m = sum[f][c] / cnt[f][c]
			}
			tot += m
			n++
			fmt.Fprintf(&b, " | %.2f", m)
		}
		fmt.Fprintf(&b, " | %.2f |\n", tot/n)
	}
	os.WriteFile(filepath.Join(dir, "judge-table.md"), []byte(b.String()), 0o644)
	fmt.Print(b.String())
}

// score runs the deterministic checks on a candidate.
func score(c *Candidate, db topics.DB) {
	c.Words, c.Uncited = 0, 0
	st := topics.Structure(c.Topic)
	gr := topics.Grounding(c.Topic, db)
	c.Structural, c.Grounding = len(st), len(gr)
	c.Problems = append(st, gr...)
	for _, p := range st {
		if strings.Contains(p, "no cite and is not marked") {
			c.Uncited++
		}
	}
	for _, cn := range []*topics.Content{&c.Topic.EN, &c.Topic.AR} {
		c.Words += len(strings.Fields(plain(cn, c.Topic, false)))
	}
}

// plain writes a content as text for a judge, citing by article number.
func plain(cn *topics.Content, t *topics.Topic, withQuotes bool) string {
	var b strings.Builder
	ref := func(ids []string) string {
		var r []string
		for _, id := range ids {
			if c := t.Cite(id); c != nil {
				r = append(r, fmt.Sprintf("law %d art. %s", c.Law, c.Article))
			}
		}
		if len(r) == 0 {
			return ""
		}
		return " [" + strings.Join(r, "; ") + "]"
	}
	fmt.Fprintf(&b, "%s\n%s\n\nANSWER: %s%s\n", cn.Title, cn.Question, cn.Answer.Text, ref(cn.Answer.Cites))
	for _, s := range cn.Sections {
		fmt.Fprintf(&b, "\n## %s\n", s.Heading)
		for _, p := range s.Paras {
			fmt.Fprintf(&b, "%s%s\n", p.Text, ref(p.Cites))
		}
		if s.Table != nil {
			fmt.Fprintf(&b, "TABLE %s: %s\n", s.Table.Caption, strings.Join(s.Table.Head, " | "))
			for _, r := range s.Table.Rows {
				fmt.Fprintf(&b, "  %s%s\n", strings.Join(r.Cells, " | "), ref(r.Cites))
			}
		}
	}
	return b.String()
}

// judgePrompt anonymizes and shuffles the candidates and asks for a
// grounded verdict: the judge lists what the articles state first, then
// scores every candidate against that list.
func judgePrompt(slug string, arts []Article, cands map[string]*Candidate) (map[string]string, string) {
	h := fnv.New64a()
	h.Write([]byte(slug))
	rng := rand.New(rand.NewSource(int64(h.Sum64())))
	var fams []string
	for _, f := range familyOrder {
		if c := cands[f]; c != nil && c.Topic != nil {
			fams = append(fams, f)
		}
	}
	rng.Shuffle(len(fams), func(i, j int) { fams[i], fams[j] = fams[j], fams[i] })
	labels := map[string]string{}
	var b strings.Builder
	b.WriteString("You are judging candidate topic pages on Lebanese law. Each page answers one question for residents and lawyers, in English and in Arabic, and may state only what the published articles below say. You are shown the articles first, then the candidates (anonymous, in random order).\n\nTHE ARTICLES (the only source of truth)\n")
	for _, a := range arts {
		fmt.Fprintf(&b, "\n=== law %d (%s), article %s ===\n%s\n", a.Law, a.Cite, a.Number, a.Text)
	}
	for i, f := range fams {
		label := string(rune('A' + i))
		labels[label] = f
		t := cands[f].Topic
		fmt.Fprintf(&b, "\n\n######## CANDIDATE %s ########\n--- ENGLISH ---\n%s\n--- ARABIC ---\n%s\n", label, plain(&t.EN, t, false), plain(&t.AR, t, false))
	}
	b.WriteString(`

TASK
Step 1. Before reading any candidate's claims as true, list from the articles alone the facts that bear on the question: "facts": ["F1: ...", "F2: ..."], each a short plain statement with its article number. Include what the articles do NOT say where that matters (a figure missing, an amendment).
Step 2. For each candidate, check every statement of its English and Arabic text against your list and the articles. Note statements that the articles do not support, that contradict them, or that overstate them ("unsupported").
Step 3. Score each candidate from 1 (poor) to 5 (excellent):
- accuracy: only supported statements, nothing invented, right figures, honest about gaps and amendments.
- clarity: a resident can read it; answer first; short sentences; plain words; no filler.
- completeness: covers what the articles let one answer on the question, uses the table where figures exist.
- arabic: the Arabic version is correct, natural standard Arabic, says what the English says.
Do not reward length. Do not prefer a candidate for style when its facts are wrong.

Reply with one JSON object and nothing else:
{"facts": ["F1: ..."], "scores": {"A": {"accuracy": 4, "clarity": 4, "completeness": 3, "arabic": 5, "unsupported": ["..."]}, "B": {...}}}`)
	return labels, b.String()
}

func report(dir string, slugs []string, cands map[string]map[string]*Candidate, verdicts func() any) {
	type out struct {
		Candidates map[string]map[string]*Candidate
		Verdicts   any
	}
	b, _ := json.MarshalIndent(out{cands, verdicts()}, "", "  ")
	os.WriteFile(filepath.Join(dir, "scores.json"), b, 0o644)
	var t strings.Builder
	t.WriteString("| topic | family | seconds | quotes | exact as written | repaired | not repairable | structural | grounding | uncited | em dashes | words |\n|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	keys := append([]string(nil), slugs...)
	sort.Strings(keys)
	for _, s := range keys {
		for _, f := range familyOrder {
			c := cands[s][f]
			if c == nil {
				continue
			}
			if c.Err != "" {
				fmt.Fprintf(&t, "| %s | %s | %.0f | failed: %.60s | | | | | | | | |\n", s, f, c.Seconds, c.Err)
				continue
			}
			fmt.Fprintf(&t, "| %s | %s | %.0f | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n", s, f, c.Seconds, c.Raw.Quotes, c.Raw.Exact, c.Raw.Repaired, c.Raw.Lost, c.Structural, c.Grounding, c.Uncited, c.Dashes, c.Words)
		}
	}
	os.WriteFile(filepath.Join(dir, "deterministic.md"), []byte(t.String()), 0o644)
	fmt.Print(t.String())
}

// rescore redoes the deterministic scoring of a finished bake-off with the
// current checks, from the pages it saved, and rewrites its tables.
func rescore(db topics.DB, slugs []string) {
	dir := filepath.Join(*workDir, "bakeoff")
	var old struct {
		Candidates map[string]map[string]*Candidate
		Verdicts   json.RawMessage
	}
	b, err := os.ReadFile(filepath.Join(dir, "scores.json"))
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(b, &old); err != nil {
		log.Fatal(err)
	}
	for slug, byFam := range old.Candidates {
		for fam, c := range byFam {
			if c.Err != "" {
				continue
			}
			tb, err := os.ReadFile(filepath.Join(dir, slug+"."+fam+".json"))
			if err != nil {
				log.Fatal(err)
			}
			c.Topic = &topics.Topic{}
			if err := json.Unmarshal(tb, c.Topic); err != nil {
				log.Fatal(err)
			}
			score(c, db)
		}
	}
	report(dir, slugs, old.Candidates, func() any { return old.Verdicts })
}
