// Command translate-en writes an unofficial English translation of the
// articles of the popular texts (data/popular_texts.txt), one JSON line per
// article, through Gemini in agy (the Antigravity CLI and its Ultra seat).
//
//	translate-en -data data -out work/article_en.jsonl [-model gemini-4-argon-high] [-workers 2] [-limit N]
//
// It is resumable: an article already in -out with the same source hash is
// skipped, so a stopped run carries on where it was. The Arabic text stays the
// official one; these lines are machine translations for readers who do not
// read Arabic (Nizar, 5 Nov 2026: "Use Gemini through AGY").
//
// The agy quota is per model and small: on RESOURCE_EXHAUSTED every worker
// stops, and the run can be started again once it resets. The LebLeg nightly
// judges with gemini-4-argon-medium, so the default here is another model.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/datatables"
	"github.com/taktekhq/lebanese-legislation/internal/judge"
)

// Line is one translated article.
type Line struct {
	LawID      int      `json:"law_id"`
	Position   int      `json:"position"`
	Number     string   `json:"number"`
	SourceHash string   `json:"source_sha256"` // of text_ar, as in the data
	En         string   `json:"en"`
	Model      string   `json:"model"`
	On         string   `json:"translated_on"`
	Checks     []string `json:"checks,omitempty"` // numbers in the Arabic that the English lacks
}

type article = datatables.Article

func main() {
	data := flag.String("data", "data", "the data/ directory")
	out := flag.String("out", "article_en.jsonl", "JSON lines to append to (read first, to skip what is done)")
	model := flag.String("model", "gemini-4-argon-high", "agy model")
	bin := flag.String("agy", "agy", "agy binary")
	workers := flag.Int("workers", 2, "agy calls at a time")
	batchChars := flag.Int("batch-chars", 2000, "Arabic characters per call, about")
	maxBatch := flag.Int("max-batch", 8, "articles per call at most")
	limit := flag.Int("limit", 0, "translate at most this many articles (0: all)")
	ids := flag.String("ids", "", "comma-separated text ids instead of data/popular_texts.txt")
	timeout := flag.Duration("timeout", 4*time.Minute, "per agy call; a call usually takes under a minute, and a stalled one is retried")
	flag.Parse()

	popular, err := readIDs(filepath.Join(*data, "popular_texts.txt"), *ids)
	if err != nil {
		fail(err)
	}
	titles, err := datatables.Titles(*data, popular)
	if err != nil {
		fail(err)
	}
	arts, err := datatables.Articles(*data, popular)
	if err != nil {
		fail(err)
	}
	done, err := readDone(*out)
	if err != nil {
		fail(err)
	}
	var todo []article
	nDoneBefore := 0
	for _, a := range arts {
		if done[key(a.Law, a.Position, a.Hash)] {
			nDoneBefore++
		}
		if !done[key(a.Law, a.Position, a.Hash)] {
			todo = append(todo, a)
		}
	}
	if *limit > 0 && len(todo) > *limit {
		todo = todo[:*limit]
	}
	batches := makeBatches(todo, *batchChars, *maxBatch)
	fmt.Fprintf(os.Stderr, "translate-en: %d texts, %d articles, %d done, %d to do in %d calls (%s)\n",
		len(popular), len(arts), nDoneBefore, len(todo), len(batches), *model)

	f, err := os.OpenFile(*out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fail(err)
	}
	defer f.Close()
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agy := &judge.Agy{Bin: *bin, Model: *model, Timeout: *timeout}
	jobs := make(chan []article)
	var wg sync.WaitGroup
	var nDone, nFail int
	var stopErr error
	start := time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// run translates a batch; a batch that fails (not on quota) is
			// split in two and each half tried again, down to one article.
			// 5 Nov 2026: batches of 25 articles (6,000 characters) stalled
			// past the timeout every time while 8 short ones took a minute,
			// and the failed batches came first in every new run.
			var run func(b []article)
			run = func(b []article) {
				if ctx.Err() != nil {
					return
				}
				lines, err := translate(ctx, agy, titles[b[0].Law], b)
				if err != nil && !quota(err) && len(lines) == 0 && len(b) > 1 {
					fmt.Fprintf(os.Stderr, "translate-en: text %d from position %d: %v; splitting the %d articles\n", b[0].Law, b[0].Position, err, len(b))
					run(b[:len(b)/2])
					run(b[len(b)/2:])
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					nFail += len(b) - len(lines)
					fmt.Fprintf(os.Stderr, "translate-en: text %d from position %d: %v\n", b[0].Law, b[0].Position, err)
					if quota(err) && stopErr == nil {
						stopErr = err
						cancel()
					}
				}
				for _, l := range lines {
					l.Model, l.On = *model, time.Now().UTC().Format("2006-01-02")
					raw, _ := json.Marshal(l)
					f.Write(append(raw, '\n'))
					nDone++
				}
				fmt.Fprintf(os.Stderr, "translate-en: %d done, %d failed, %s\n", nDone, nFail, time.Since(start).Round(time.Second))
			}
			for b := range jobs {
				run(b)
			}
		}()
	}
	for _, b := range batches {
		if ctx.Err() != nil {
			break
		}
		jobs <- b
	}
	close(jobs)
	wg.Wait()
	if stopErr != nil {
		fmt.Fprintf(os.Stderr, "translate-en: stopped, agy quota: %v\n", stopErr)
		os.Exit(3)
	}
	if nFail > 0 {
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "translate-en:", err)
	os.Exit(2)
}

func quota(err error) bool {
	s := err.Error()
	return strings.Contains(s, "RESOURCE_EXHAUSTED") || strings.Contains(s, "quota") || strings.Contains(s, "429")
}

func key(law, pos int, hash string) string { return fmt.Sprintf("%d/%d/%s", law, pos, hash) }

const system = `You translate Lebanese legislation from Arabic into English.
Translate faithfully and completely, sentence by sentence, in the register of an English statute. Do not summarise, explain, modernise, correct or add anything, and do not leave anything out. Keep every number, date, amount, article reference and list numbering exactly as written. Keep the paragraph breaks. Translate only the article's text: do not add a heading such as "Article 5". Use the standard English legal term where one exists. If a word is unclear or seems mistyped, translate it literally.
Do not use any tool, run any command or write any file: reason from the text alone.`

var schema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"articles": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":       "object",
				"properties": map[string]any{"key": map[string]any{"type": "string"}, "en": map[string]any{"type": "string"}},
				"required":   []string{"key", "en"},
			},
		},
	},
	"required": []string{"articles"},
}

func translate(ctx context.Context, agy *judge.Agy, title string, b []article) ([]Line, error) {
	var p strings.Builder
	fmt.Fprintf(&p, "The articles below are from the Lebanese text: %s\nTranslate each one. Return one entry per article with the same key.\n\n", title)
	for _, a := range b {
		fmt.Fprintf(&p, "### key: %d\n%s\n\n", a.Position, a.Text)
	}
	var got struct {
		Articles []struct {
			Key string `json:"key"`
			En  string `json:"en"`
		} `json:"articles"`
	}
	if _, _, err := agy.Ask(ctx, system, p.String(), schema, &got); err != nil {
		return nil, err
	}
	byKey := map[string]string{}
	for _, a := range got.Articles {
		byKey[strings.TrimSpace(a.Key)] = strings.TrimSpace(a.En)
	}
	var out []Line
	var missing []string
	for _, a := range b {
		en := byKey[strconv.Itoa(a.Position)]
		if en == "" {
			missing = append(missing, a.Number)
			continue
		}
		out = append(out, Line{LawID: a.Law, Position: a.Position, Number: a.Number, SourceHash: a.Hash, En: en, Checks: numberChecks(a.Text, en)})
	}
	if len(missing) > 0 {
		return out, fmt.Errorf("no translation for articles %s", strings.Join(missing, ", "))
	}
	return out, nil
}

var digits = strings.NewReplacer("٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9")
var numRe = regexp.MustCompile(`\d+`)
var monthRe = regexp.MustCompile(`[كت]\s*[12١٢]\b`)
var headRe = regexp.MustCompile(`^\s*(?:ال)?مادة\s+[0-9٠-٩]+[^:\n]{0,12}:`)

// numberChecks lists the numbers of the Arabic text that the English does
// not contain, for a reviewer: a dropped amount or date is the error that
// matters most in a law. Two kinds are not errors and are left out: the
// article's own number (many texts start "مادة 207:", a heading the English
// leaves out) and a month written as ك1, ك2, ت1, ت2 (December, January,
// October, November in the English).
func numberChecks(ar, en string) []string {
	ar = monthRe.ReplaceAllString(ar, " ")
	ar = headRe.ReplaceAllString(ar, "")
	have := map[string]bool{}
	for _, n := range numRe.FindAllString(strings.ReplaceAll(en, ",", ""), -1) {
		have[strings.TrimLeft(n, "0")] = true
	}
	var miss []string
	seen := map[string]bool{}
	for _, n := range numRe.FindAllString(digits.Replace(ar), -1) {
		n = strings.TrimLeft(n, "0")
		if n == "" || have[n] || seen[n] {
			continue
		}
		seen[n] = true
		miss = append(miss, n)
	}
	return miss
}

func makeBatches(arts []article, chars, max int) [][]article {
	var out [][]article
	var cur []article
	n := 0
	for _, a := range arts {
		l := len([]rune(a.Text))
		if len(cur) > 0 && (cur[0].Law != a.Law || n+l > chars || len(cur) >= max) {
			out = append(out, cur)
			cur, n = nil, 0
		}
		cur = append(cur, a)
		n += l
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func readIDs(path, list string) (map[int]bool, error) {
	raw := list
	if raw == "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		raw = string(b)
	}
	ids := map[int]bool{}
	var fields []string
	for _, line := range strings.Split(raw, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		fields = append(fields, strings.Split(line, ",")...)
	}
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		id, err := strconv.Atoi(f)
		if err != nil {
			return nil, fmt.Errorf("%s: not a text id: %q", path, f)
		}
		ids[id] = true
	}
	return ids, nil
}

func readDone(path string) (map[string]bool, error) {
	done := map[string]bool{}
	fh, err := os.Open(path)
	if os.IsNotExist(err) {
		return done, nil
	}
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var l Line
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.En != "" {
			done[key(l.LawID, l.Position, l.SourceHash)] = true
		}
	}
	return done, sc.Err()
}
