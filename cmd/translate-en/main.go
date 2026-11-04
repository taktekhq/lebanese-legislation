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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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

type article struct {
	law, pos int
	number   string
	text     string
	hash     string
}

func main() {
	data := flag.String("data", "data", "the data/ directory")
	out := flag.String("out", "article_en.jsonl", "JSON lines to append to (read first, to skip what is done)")
	model := flag.String("model", "gemini-4-argon-high", "agy model")
	bin := flag.String("agy", "agy", "agy binary")
	workers := flag.Int("workers", 2, "agy calls at a time")
	batchChars := flag.Int("batch-chars", 6000, "Arabic characters per call, about")
	maxBatch := flag.Int("max-batch", 25, "articles per call at most")
	limit := flag.Int("limit", 0, "translate at most this many articles (0: all)")
	ids := flag.String("ids", "", "comma-separated text ids instead of data/popular_texts.txt")
	flag.Parse()

	popular, err := readIDs(filepath.Join(*data, "popular_texts.txt"), *ids)
	if err != nil {
		fail(err)
	}
	titles, err := readTitles(filepath.Join(*data, "tables", "legislation"), popular)
	if err != nil {
		fail(err)
	}
	arts, err := readArticles(filepath.Join(*data, "tables", "legislation_article"), popular)
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
		if done[key(a.law, a.pos, a.hash)] {
			nDoneBefore++
		}
		if !done[key(a.law, a.pos, a.hash)] {
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
	agy := &judge.Agy{Bin: *bin, Model: *model, Dir: os.TempDir(), Timeout: 10 * time.Minute}
	jobs := make(chan []article)
	var wg sync.WaitGroup
	var nDone, nFail int
	var stopErr error
	start := time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range jobs {
				if ctx.Err() != nil {
					continue
				}
				lines, err := translate(ctx, agy, titles[b[0].law], b)
				mu.Lock()
				if err != nil {
					nFail += len(b)
					fmt.Fprintf(os.Stderr, "translate-en: text %d from position %d: %v\n", b[0].law, b[0].pos, err)
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
				mu.Unlock()
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
		fmt.Fprintf(&p, "### key: %d\n%s\n\n", a.pos, a.text)
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
		en := byKey[strconv.Itoa(a.pos)]
		if en == "" {
			missing = append(missing, a.number)
			continue
		}
		out = append(out, Line{LawID: a.law, Position: a.pos, Number: a.number, SourceHash: a.hash, En: en, Checks: numberChecks(a.text, en)})
	}
	if len(missing) > 0 {
		return out, fmt.Errorf("no translation for articles %s", strings.Join(missing, ", "))
	}
	return out, nil
}

var digits = strings.NewReplacer("٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9")
var numRe = regexp.MustCompile(`\d+`)

// numberChecks lists the numbers of the Arabic text that the English does
// not contain, for a reviewer: a dropped amount or date is the error that
// matters most in a law.
func numberChecks(ar, en string) []string {
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
		l := len([]rune(a.text))
		if len(cur) > 0 && (cur[0].law != a.law || n+l > chars || len(cur) >= max) {
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

// eachRow calls fn with the fields of every row of a table directory (PostgreSQL
// COPY text format, the COLUMNS file names them).
func eachRow(dir string, fn func(col map[string]int, f []string) error) error {
	cols, err := os.ReadFile(filepath.Join(dir, "COLUMNS"))
	if err != nil {
		return err
	}
	col := map[string]int{}
	for i, c := range strings.Split(strings.TrimSpace(string(cols)), ",") {
		col[c] = i
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.tsv"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, name := range files {
		fh, err := os.Open(name)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			f := strings.Split(sc.Text(), "\t")
			for i := range f {
				f[i] = unescape(f[i])
			}
			if err := fn(col, f); err != nil {
				fh.Close()
				return err
			}
		}
		fh.Close()
		if err := sc.Err(); err != nil {
			return err
		}
	}
	return nil
}

var copyEsc = strings.NewReplacer(`\\`, `\`, `\n`, "\n", `\t`, "\t", `\r`, "\r")

func unescape(s string) string {
	if s == `\N` {
		return ""
	}
	return copyEsc.Replace(s)
}

func readTitles(dir string, ids map[int]bool) (map[int]string, error) {
	out := map[int]string{}
	err := eachRow(dir, func(col map[string]int, f []string) error {
		id, _ := strconv.Atoi(f[col["id"]])
		if ids[id] {
			out[id] = f[col["title"]]
		}
		return nil
	})
	return out, err
}

func readArticles(dir string, ids map[int]bool) ([]article, error) {
	var out []article
	err := eachRow(dir, func(col map[string]int, f []string) error {
		law, _ := strconv.Atoi(f[col["law_id"]])
		if !ids[law] {
			return nil
		}
		text := strings.TrimSpace(f[col["text_ar"]])
		if text == "" {
			return nil
		}
		pos, _ := strconv.Atoi(f[col["position"]])
		sum := sha256.Sum256([]byte(f[col["text_ar"]]))
		out = append(out, article{law: law, pos: pos, number: f[col["number"]], text: text, hash: hex.EncodeToString(sum[:])})
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].law != out[j].law {
			return out[i].law < out[j].law
		}
		return out[i].pos < out[j].pos
	})
	return out, err
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
