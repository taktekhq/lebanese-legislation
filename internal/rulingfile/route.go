package rulingfile

// Routing: each page goes to the cheapest reader that reads it well. Typed
// pages (most of the archive) go to Claude on a subscription; handwritten
// and badly faded pages go to Gemini through the paid API, where every page
// costs money. Tesseract's mean word confidence on the page tells them apart
// for free: on a 60-page sample (21 Oct 2026) handwriting scored 29-39,
// worn typewriter 41-51, clean type 56-88.
//
// The stages share one folder and are safe to run at the same time:
//
//	triage  renders each page to the cache and scores it (<id>.triage.json)
//	claude  Opus, Sonnet and Haiku read the pages that need them
//	gemini  Gemini reads handwriting and the typed pages a vote left open, in Batch jobs
//
// Each reading is stored as it came (pipeline.go) and the page is published
// once its readers are in, by majority vote (vote.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Tiers: what kind of page it is, which decides its readers (PagePlan).
const (
	TierHaiku  = "haiku"
	TierSonnet = "sonnet"
	TierOpus   = "opus"
	TierGemini = "gemini"
)

// Router holds the thresholds. A page with fewer than MinWords words is
// near blank (a stamp, a signature) and goes to the cheapest tier.
type Router struct {
	HaikuConf  int // mean confidence at or above this: Haiku
	SonnetConf int // at or above this (and below HaikuConf): Sonnet; below: Gemini
	MinWords   int
	// Important rulings (the Constitutional and Judicial Councils, by
	// default) get Gemini as a fourth reader on every page.
	Important map[int]bool
	// First are read before the rest (the rulings with no summary, whose
	// judgment is their only text).
	First map[int]bool
}

// firstSort puts the First rulings ahead of the others, each group in id order.
func (r Router) firstSort(ids []int) {
	sort.SliceStable(ids, func(i, j int) bool { return r.First[ids[i]] && !r.First[ids[j]] })
}

// DefaultRouter is calibrated on the 21 Oct 2026 sample.
var DefaultRouter = Router{HaikuConf: 60, SonnetConf: 45, MinWords: 15}

// Tier picks a page's reader.
func (r Router) Tier(id int, p TriagePage) string {
	t := TierGemini
	switch {
	case p.Words < r.MinWords || p.Conf >= r.HaikuConf:
		t = TierHaiku
	case p.Conf >= r.SonnetConf:
		t = TierSonnet
	}
	return t
}

// Triage is a ruling's pages with their Tesseract scores.
type Triage struct {
	ID    int          `json:"id"`
	DPI   int          `json:"dpi"`
	Pages []TriagePage `json:"pages"`
}

// TriagePage is one page's score: mean word confidence (0-100) and the number
// of words Tesseract found.
type TriagePage struct {
	N     int `json:"n"`
	Conf  int `json:"conf"`
	Words int `json:"words"`
	// Blank: the image has almost no ink (a blank scan, a scanner edge on
	// white). Such a page is published empty and never sent to a reader.
	Blank bool `json:"blank,omitempty"`
}

// blankInk is the share of dark pixels, in the middle 80% of the page, below
// which a page is blank. Measured 27 Oct 2026 on the cache: the 11 blank scans
// of ruling-ocr's unread list give 0.00000 to 0.00001; printed or handwritten
// pages give 0.03 to 0.07.
const blankInk = 0.001

// IsBlankImage reports whether the page image has almost no ink. The 10%
// margins are left out: a scanner edge shows there, not on the sheet.
func IsBlankImage(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	im, _, err := image.Decode(f)
	if err != nil {
		return false, err
	}
	b := im.Bounds()
	mx, my := b.Dx()/10, b.Dy()/10
	dark, total := 0, 0
	for y := b.Min.Y + my; y < b.Max.Y-my; y++ {
		for x := b.Min.X + mx; x < b.Max.X-mx; x++ {
			r, _, _, _ := im.At(x, y).RGBA()
			total++
			if r>>8 < 128 {
				dark++
			}
		}
	}
	return total > 0 && float64(dark)/float64(total) < blankInk, nil
}

// TriagePath is where a ruling's triage is kept under dir.
func TriagePath(dir string, id int) string {
	return strings.TrimSuffix(Path(dir, id), ".pdf") + ".triage.json"
}

// PageImage is a rendered page in the cache.
func PageImage(cache string, id, n int) string {
	return filepath.Join(cache, strconv.Itoa(id), fmt.Sprintf("p-%d.jpg", n))
}

// Downloaded lists the ids with a PDF in dir, in id order.
func Downloaded(dir string) ([]int, error) {
	var ids []int
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".pdf") {
			return err
		}
		if id, err := strconv.Atoi(strings.TrimSuffix(d.Name(), ".pdf")); err == nil {
			ids = append(ids, id)
		}
		return nil
	})
	sort.Ints(ids)
	return ids, err
}

// LoadTriage reads a ruling's triage; nil when it has none yet.
func LoadTriage(dir string, id int) (*Triage, error) {
	b, err := os.ReadFile(TriagePath(dir, id))
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var t Triage
	return &t, json.Unmarshal(b, &t)
}

// LoadReading reads a ruling's reading; nil when it has none yet.
func LoadReading(dir string, id int) (*Reading, error) {
	b, err := os.ReadFile(ReadPath(dir, id))
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var rd Reading
	return &rd, json.Unmarshal(b, &rd)
}

// Triager renders pages to the cache and scores them with Tesseract.
type Triager struct {
	Dir, Cache string
	DPI        int
	Workers    int
	Router     Router // its First rulings are scored first
	Meta       map[int]Meta
	Log        func(format string, args ...any)
}

// Run triages every downloaded ruling that has no triage yet.
func (t *Triager) Run(ctx context.Context) error {
	ids, err := Downloaded(t.Dir)
	if err != nil {
		return err
	}
	var todo []int
	for _, id := range ids {
		if _, err := os.Stat(TriagePath(t.Dir, id)); os.IsNotExist(err) {
			todo = append(todo, id)
		}
	}
	t.Router.firstSort(todo)
	t.Log("triage: %d rulings to score", len(todo))
	start := time.Now()
	var mu sync.Mutex
	n, pages := 0, 0
	tiers := map[string]int{}
	err = parallel(ctx, todo, t.Workers, func(id int) {
		tr, err := t.one(ctx, id)
		mu.Lock()
		defer mu.Unlock()
		n++
		if err != nil {
			t.Log("triage %d: %v", id, err)
			return
		}
		for _, p := range tr.Pages {
			tiers[DefaultRouter.Tier(id, p)]++
		}
		pages += len(tr.Pages)
		if n%200 == 0 {
			t.Log("triage: %d/%d rulings, %d pages (haiku %d, sonnet %d, gemini %d), %.0f pages/min",
				n, len(todo), pages, tiers[TierHaiku], tiers[TierSonnet], tiers[TierGemini], float64(pages)/time.Since(start).Minutes())
		}
	})
	t.Log("triage: done %d rulings, %d pages (haiku %d, sonnet %d, gemini %d)", n, pages, tiers[TierHaiku], tiers[TierSonnet], tiers[TierGemini])
	return err
}

func (t *Triager) one(ctx context.Context, id int) (*Triage, error) {
	dir := filepath.Join(t.Cache, strconv.Itoa(id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if out, err := exec.CommandContext(ctx, "pdftoppm", "-r", strconv.Itoa(t.DPI), "-gray", "-jpeg", Path(t.Dir, id), filepath.Join(dir, "r")).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdftoppm: %v %s", err, out)
	}
	imgs, _ := filepath.Glob(filepath.Join(dir, "r-*.jpg"))
	if len(imgs) == 0 {
		return nil, fmt.Errorf("no pages")
	}
	tr := &Triage{ID: id, DPI: t.DPI}
	texts := map[int]string{}
	for _, img := range imgs {
		n := pageNum(img)
		if err := os.Rename(img, PageImage(t.Cache, id, n)); err != nil {
			return nil, err
		}
		conf, words, text, err := tesseractRead(ctx, PageImage(t.Cache, id, n))
		if err != nil {
			return nil, err
		}
		tp := TriagePage{N: n, Conf: conf, Words: words}
		if words == 0 {
			tp.Blank, _ = IsBlankImage(PageImage(t.Cache, id, n))
		}
		tr.Pages = append(tr.Pages, tp)
		texts[n] = text
	}
	sort.Slice(tr.Pages, func(i, j int) bool { return tr.Pages[i].N < tr.Pages[j].N })
	b, err := json.Marshal(tr)
	if err != nil {
		return nil, err
	}
	if err := writeFile(TriagePath(t.Dir, id), b); err != nil {
		return nil, err
	}
	// Tesseract's text is a free voter on typed pages; on handwriting it is noise.
	typed := map[int]string{}
	for _, p := range tr.Pages {
		if t.Router.Tier(id, p) != TierGemini && texts[p.N] != "" {
			typed[p.N] = texts[p.N]
		}
	}
	return tr, t.Router.AddReadings(t.Dir, id, Tesseract, typed, tr, t.Meta[id])
}

// MarkBlank flags, in rulings already triaged, the pages with no words whose
// image has almost no ink, and publishes them as blank. No model is called.
func (t *Triager) MarkBlank() error {
	ids, err := Downloaded(t.Dir)
	if err != nil {
		return err
	}
	n := 0
	for _, id := range ids {
		tr, _ := LoadTriage(t.Dir, id)
		if tr == nil {
			continue
		}
		changed := false
		for i, p := range tr.Pages {
			if p.Blank || p.Words > 0 {
				continue
			}
			if blank, err := IsBlankImage(PageImage(t.Cache, id, p.N)); err == nil && blank {
				tr.Pages[i].Blank, changed = true, true
				n++
				t.Log("blank: ruling %d page %d", id, p.N)
			}
		}
		if !changed {
			continue
		}
		b, err := json.Marshal(tr)
		if err != nil {
			return err
		}
		if err := writeFile(TriagePath(t.Dir, id), b); err != nil {
			return err
		}
		if err := t.Router.AddReadings(t.Dir, id, "", nil, tr, t.Meta[id]); err != nil {
			return err
		}
	}
	t.Log("blank: %d pages marked", n)
	return nil
}

// Backfill gives the rulings scored before Tesseract's text was kept their
// Tesseract readings (typed pages only).
func (t *Triager) Backfill(ctx context.Context) error {
	ids, err := Downloaded(t.Dir)
	if err != nil {
		return err
	}
	var todo []int
	for _, id := range ids {
		tr, _ := LoadTriage(t.Dir, id)
		if tr == nil {
			continue
		}
		rd, _ := LoadReading(t.Dir, id)
		rs, _ := LoadReadings(t.Dir, id)
		if len(t.Router.Missing(tr, rd, rs, Tesseract)) > 0 {
			todo = append(todo, id)
		}
	}
	t.Router.firstSort(todo)
	t.Log("tesseract: %d rulings to read", len(todo))
	var mu sync.Mutex
	n := 0
	return parallel(ctx, todo, t.Workers, func(id int) {
		tr, _ := LoadTriage(t.Dir, id)
		rd, _ := LoadReading(t.Dir, id)
		rs, _ := LoadReadings(t.Dir, id)
		texts := map[int]string{}
		for _, p := range t.Router.Missing(tr, rd, rs, Tesseract) {
			if _, _, text, err := tesseractRead(ctx, PageImage(t.Cache, id, p)); err == nil && text != "" {
				texts[p] = text
			}
		}
		err := t.Router.AddReadings(t.Dir, id, Tesseract, texts, tr, t.Meta[id])
		mu.Lock()
		defer mu.Unlock()
		if n++; err != nil {
			t.Log("tesseract %d: %v", id, err)
		} else if n%500 == 0 {
			t.Log("tesseract: %d/%d rulings", n, len(todo))
		}
	})
}

// tesseractRead runs Tesseract (Arabic) on an image and returns the mean
// confidence of the words it found (one-letter words left out), their count,
// and its text, line by line.
func tesseractRead(ctx context.Context, img string) (int, int, string, error) {
	out, err := exec.CommandContext(ctx, "tesseract", img, "-", "-l", "ara", "--psm", "6", "tsv").Output()
	if err != nil {
		return 0, 0, "", fmt.Errorf("tesseract: %v", err)
	}
	conf, n, err := ParseTSVConf(string(out))
	return conf, n, TSVText(string(out)), err
}

// TSVText rebuilds the text from Tesseract's TSV, one line per line it saw.
func TSVText(tsv string) string {
	var b strings.Builder
	last := ""
	for i, line := range strings.Split(tsv, "\n") {
		f := strings.Split(line, "\t")
		if i == 0 || len(f) < 12 || f[0] != "5" || strings.TrimSpace(f[11]) == "" {
			continue
		}
		at := f[2] + "." + f[3] + "." + f[4] // block, paragraph, line
		switch {
		case last == "":
		case at != last:
			b.WriteString("\n")
		default:
			b.WriteString(" ")
		}
		b.WriteString(strings.TrimSpace(f[11]))
		last = at
	}
	return b.String()
}

// ParseTSVConf averages the word confidences of Tesseract's TSV output.
func ParseTSVConf(tsv string) (int, int, error) {
	sum, n := 0.0, 0
	for i, line := range strings.Split(tsv, "\n") {
		f := strings.Split(line, "\t")
		if i == 0 || len(f) < 12 || len([]rune(strings.TrimSpace(f[11]))) < 2 {
			continue
		}
		c, err := strconv.ParseFloat(f[10], 64)
		if err != nil || c < 0 {
			continue
		}
		sum += c
		n++
	}
	if n == 0 {
		return 0, 0, nil
	}
	return int(sum/float64(n) + 0.5), n, nil
}

// parallel runs fn on each id with n workers until ctx ends.
func parallel(ctx context.Context, ids []int, n int, fn func(int)) error {
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				fn(id)
			}
		}()
	}
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	return ctx.Err()
}
