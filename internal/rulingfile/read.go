package rulingfile

// Reading: the judgments are scans (older ones handwritten), so the text comes
// from Gemini through agy, the Antigravity CLI on a Google AI subscription.
// Anything legal is read by Gemini (since 16 Oct 2026); the model transcribes
// and never rewrites. One call reads up to ChunkPages pages: agy is slow to
// start, so whole rulings per call is ~8x faster than a page per call.
//
// Each reading lands next to its PDF as <id>.read.json, written once; a run
// skips rulings already read, so it can stop at any point.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/agentcli"
)

// PromptVersion changes whenever the prompt does, so readings can be redone.
const PromptVersion = 1

const readPrompt = `View each of these image files exactly once with your file viewer, in this order: %s. Do not crop, zoom, resize or run any code or tool other than those views. They are pages %d to %d of one Lebanese court judgment (%d pages in all), Arabic, often handwritten. Transcribe each page as written, line by line in reading order, including the header box and margin notes (prefix margin lines with [هامش]). Start each page with a line '=== صفحة N ===' using its page number in the judgment. Write [؟] for any word you cannot read; never guess, correct or complete. Output only the transcription.`

// Reading is one ruling's machine reading, page by page.
type Reading struct {
	ID      int       `json:"id"`
	Model   string    `json:"model"`
	Prompt  int       `json:"prompt_version"`
	ReadAt  time.Time `json:"read_at"`
	Seconds float64   `json:"seconds"`
	Pages   []Page    `json:"pages"`
	// HeaderMismatch: the first pages don't show the Centre's number and year
	// or president even after every reader; listed for a person to check.
	HeaderMismatch bool `json:"header_mismatch,omitempty"`
}

// Page is one page's text. Missing is set when the model skipped the page.
type Page struct {
	N       int    `json:"n"`
	Text    string `json:"text"`
	Missing bool   `json:"missing,omitempty"`
	// Blank: the scan has no ink; the empty text is the reading, not a gap.
	Blank bool `json:"blank,omitempty"`
	// Model names the reader, or the readers joined by "+" when the text is
	// their vote (pipeline.go); empty means the Reading's Model.
	Model string `json:"model,omitempty"`
	// LowConfidence: the page is hard to read (handwriting, by Tesseract's
	// triage, or a model declined it), so its text may hold many errors. Printed
	// pages are trusted, read once or voted; handwriting stays low until a
	// handwriting model (Dactelo) reads it.
	LowConfidence bool `json:"low_confidence,omitempty"`
	// How the vote went: words in reading A, words A and B wrote alike,
	// disputed spans a majority settled, and spans left marked with both readings.
	Words    int `json:"words,omitempty"`
	Agreed   int `json:"agreed,omitempty"`
	Settled  int `json:"settled,omitempty"`
	Disputed int `json:"disputed,omitempty"`
	// Want: readers the last merge asked for (a dispute left, or a header
	// that doesn't match the record); the page is final once it is empty.
	Want []string `json:"want,omitempty"`
	// NeedGemini is the first plan's Want ["gemini"], read from old files.
	NeedGemini bool `json:"need_gemini,omitempty"`
}

// Voted says the page's text is a vote of its readers (their names joined by
// "+"), not one reader's text shown before the others came.
func (p Page) Voted() bool { return p.Blank || strings.Contains(p.Model, "+") }

// BlankModel is the Model of a blank page: no reader saw it.
const BlankModel = "blank"

// ReadPath is where a ruling's reading is kept under dir.
func ReadPath(dir string, id int) string {
	return strings.TrimSuffix(Path(dir, id), ".pdf") + ".read.json"
}

var pageMark = regexp.MustCompile(`(?m)^\s*=+\s*صفحة\s*([0-9٠-٩]+)\s*=+\s*$`)

// SplitPages cuts a transcription at its '=== صفحة N ===' lines.
func SplitPages(s string) map[int]string {
	out := map[int]string{}
	locs := pageMark.FindAllStringSubmatchIndex(s, -1)
	for i, l := range locs {
		n, err := strconv.Atoi(arabicDigits(s[l[2]:l[3]]))
		if err != nil {
			continue
		}
		end := len(s)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out[n] = strings.TrimSpace(s[l[1]:end])
	}
	return out
}

// onePage: asked for one page, the answer is that page whatever number the
// model wrote on it (asked for page 3 alone, Gemini labelled it 2, and the
// reading was dropped; 24 Oct 2026).
func onePage(got map[int]string, want []int) map[int]string {
	if len(want) == 1 && len(got) == 1 {
		for _, t := range got {
			return map[int]string{want[0]: t}
		}
	}
	return got
}

func arabicDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '٠' && r <= '٩' {
			return '0' + (r - '٠')
		}
		return r
	}, s)
}

// Reader reads downloaded PDFs with agy.
type Reader struct {
	Dir        string // where ruling-files saved the PDFs
	Agy        string // agy binary
	Model      string // e.g. gemini-3.8-flash-low
	Workers    int    // agy image jobs at once (the subscription handles 6-8)
	ChunkPages int
	DPI        int
	Timeout    time.Duration // per agy call
	Log        func(format string, args ...any)
}

// ErrQuota means the subscription's quota ran out; the caller should wait.
var ErrQuota = errors.New("agy quota reached")

// Pending lists ids whose PDF is downloaded and not yet read, in id order.
func (r *Reader) Pending() ([]int, error) {
	var ids []int
	err := filepath.WalkDir(r.Dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".pdf") {
			return err
		}
		id, err := strconv.Atoi(strings.TrimSuffix(d.Name(), ".pdf"))
		if err != nil {
			return nil
		}
		if _, err := os.Stat(ReadPath(r.Dir, id)); os.IsNotExist(err) {
			ids = append(ids, id)
		}
		return nil
	})
	sort.Ints(ids)
	return ids, err
}

// Run reads every pending ruling. On a quota error it sleeps and carries on.
func (r *Reader) Run(ctx context.Context, ids []int) error {
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var n, failed, pages int
	start := time.Now()
	errLog, err := os.OpenFile(filepath.Join(r.Dir, "read-errors.tsv"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer errLog.Close()
	for w := 0; w < r.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				rd, err := r.One(ctx, id)
				for errors.Is(err, ErrQuota) && ctx.Err() == nil {
					r.Log("ruling %d: quota reached, waiting 20 min", id)
					select {
					case <-time.After(20 * time.Minute):
					case <-ctx.Done():
					}
					rd, err = r.One(ctx, id)
				}
				mu.Lock()
				n++
				if err != nil {
					failed++
					fmt.Fprintf(errLog, "%d\t%s\t%s\n", id, time.Now().UTC().Format(time.RFC3339), strings.ReplaceAll(err.Error(), "\n", " "))
				} else {
					pages += len(rd.Pages)
				}
				if n%50 == 0 {
					el := time.Since(start)
					r.Log("%d/%d rulings read (%d pages, %d failed), %.1f rulings/h", n, len(ids), pages, failed, float64(n)/el.Hours())
				}
				mu.Unlock()
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
	r.Log("read %d rulings (%d pages), %d failed, in %s", n-failed, pages, failed, time.Since(start).Round(time.Second))
	return ctx.Err()
}

// One reads one ruling and saves its reading.
func (r *Reader) One(ctx context.Context, id int) (*Reading, error) {
	pdf := Path(r.Dir, id)
	work, err := os.MkdirTemp("", fmt.Sprintf("ruling-%d-", id))
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	if out, err := exec.CommandContext(ctx, "pdftoppm", "-r", strconv.Itoa(r.DPI), "-gray", "-jpeg", pdf, filepath.Join(work, "p")).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdftoppm: %v %s", err, out)
	}
	imgs, _ := filepath.Glob(filepath.Join(work, "p-*.jpg"))
	sort.Slice(imgs, func(i, j int) bool { return pageNum(imgs[i]) < pageNum(imgs[j]) })
	if len(imgs) == 0 {
		return nil, fmt.Errorf("no pages")
	}
	rd := &Reading{ID: id, Model: r.Model, Prompt: PromptVersion, ReadAt: time.Now().UTC()}
	t0 := time.Now()
	for lo := 0; lo < len(imgs); lo += r.ChunkPages {
		hi := min(lo+r.ChunkPages, len(imgs))
		got, err := r.chunk(ctx, work, imgs[lo:hi], lo+1, len(imgs))
		if err != nil {
			return nil, err
		}
		for p := lo + 1; p <= hi; p++ {
			t, ok := got[p]
			rd.Pages = append(rd.Pages, Page{N: p, Text: t, Missing: !ok})
		}
	}
	rd.Seconds = time.Since(t0).Seconds()
	missing := 0
	for _, p := range rd.Pages {
		if p.Missing {
			missing++
		}
	}
	if missing*2 > len(rd.Pages) {
		return nil, fmt.Errorf("%d of %d pages missing from the answer", missing, len(rd.Pages))
	}
	b, err := json.MarshalIndent(rd, "", " ")
	if err != nil {
		return nil, err
	}
	return rd, writeFile(ReadPath(r.Dir, id), b)
}

func pageNum(path string) int {
	b := strings.TrimSuffix(filepath.Base(path), ".jpg")
	n, _ := strconv.Atoi(b[strings.LastIndex(b, "-")+1:])
	return n
}

// chunk asks agy for pages first..first+len(imgs)-1; a page the answer lacks
// is retried once on its own.
func (r *Reader) chunk(ctx context.Context, dir string, imgs []string, first, total int) (map[int]string, error) {
	got, err := r.call(ctx, dir, imgs, first, total)
	if err != nil {
		return nil, err
	}
	for i, img := range imgs {
		if _, ok := got[first+i]; ok || len(imgs) == 1 {
			continue
		}
		one, err := r.call(ctx, dir, []string{img}, first+i, total)
		if err != nil {
			return nil, err
		}
		if t, ok := one[first+i]; ok {
			got[first+i] = t
		} else if len(one) == 1 { // one page asked, one answered under another number
			for _, t := range one {
				got[first+i] = t
			}
		}
	}
	return got, nil
}

// command builds one agy run over the page images in dir. The pages come
// from a third-party site, so agy runs in dir, a private folder holding only
// those images, with a minimal environment (agentcli.Env), and with
// permissions that let it read dir and nothing else: commands, writes, the
// web and MCP tools are denied, and any other tool needs a grant that
// headless -p mode cannot give.
func (r *Reader) command(ctx context.Context, dir, prompt string) *exec.Cmd {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	cmd := exec.CommandContext(ctx, r.Agy, "-p", prompt, "--model", r.Model, "--print-timeout", "0",
		"--output-format", "json", "--disable-slash-commands", "--settings", agentcli.AgySettings(dir))
	cmd.Dir = dir
	cmd.Env = agentcli.Env(os.Environ())
	return cmd
}

func (r *Reader) call(ctx context.Context, dir string, imgs []string, first, total int) (map[int]string, error) {
	names := make([]string, len(imgs))
	for i, p := range imgs {
		names[i] = filepath.Base(p)
	}
	prompt := fmt.Sprintf(readPrompt, strings.Join(names, ", "), first, first+len(imgs)-1, total)
	var last error
	for try := 0; try < 2; try++ {
		cctx, cancel := context.WithTimeout(ctx, r.Timeout)
		out, err := r.command(cctx, dir, prompt).Output()
		cancel()
		var ans struct {
			Status   string `json:"status"`
			Response string `json:"response"`
			Error    any    `json:"error"`
		}
		if jerr := json.Unmarshal(out, &ans); jerr != nil {
			last = fmt.Errorf("agy: %v, %d bytes: %.200s", err, len(out), out)
			continue
		}
		if ans.Status != "SUCCESS" {
			msg := fmt.Sprint(ans.Error)
			if strings.Contains(strings.ToLower(msg), "quota") {
				return nil, fmt.Errorf("%w: %s", ErrQuota, msg)
			}
			last = fmt.Errorf("agy %s: %.300s", ans.Status, msg)
			continue
		}
		pages := SplitPages(ans.Response)
		if len(pages) == 0 && len(imgs) == 1 && strings.TrimSpace(ans.Response) != "" {
			pages = map[int]string{first: strings.TrimSpace(ans.Response)}
		}
		return pages, nil
	}
	return nil, last
}
