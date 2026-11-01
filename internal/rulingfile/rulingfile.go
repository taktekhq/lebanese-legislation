// Package rulingfile downloads the full judgments behind the Centre's ruling
// pages. A ruling page links «نص الحكم كاملا» to RulingFile.aspx?RuliID=N,
// which answers with the judgment as a PDF (in practice a scan with no text
// layer), or with an empty form when the Centre holds no file.
//
// Files land in <dir>/<id÷1000>/<id>.pdf and every outcome is appended to
// <dir>/index.tsv, so a run can stop at any point and the next one carries on.
package rulingfile

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/archive"
)

// Outcomes recorded in index.tsv.
const (
	OK    = "ok"    // a PDF, saved
	None  = "none"  // the Centre answered with no file
	Other = "other" // something else (an HTML file list?), saved as <id>.html for a look
	Error = "error" // network or server error; retried on the next run
)

var linkRe = regexp.MustCompile(`RulingFile\.aspx\?RuliID=(\d+)`)

// Linked returns the ids of archived ruling pages that link to a file, in id order.
func Linked(a archive.Archive) ([]int, error) {
	entries, err := a.Index("ruling")
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, e := range entries {
		page, err := a.Read("ruling", e.ID)
		if err != nil {
			return nil, err
		}
		if linkRe.Match(page) {
			ids = append(ids, e.ID)
		}
	}
	return ids, nil
}

// Classify tells what a RulingFile.aspx answer holds.
func Classify(body []byte) string {
	if bytes.HasPrefix(body, []byte("%PDF-")) {
		return OK
	}
	// The no-file answer is the bare ASP.NET form: a viewstate and an empty div.
	if bytes.Contains(body, []byte(`id="form1"`)) && !bytes.Contains(body, []byte("href=")) {
		return None
	}
	return Other
}

// Path is where a ruling's PDF is kept under dir.
func Path(dir string, id int) string {
	return filepath.Join(dir, fmt.Sprintf("%04d", id/1000), strconv.Itoa(id)+".pdf")
}

// Done reads index.tsv and returns the ids already settled (ok, none, other).
// Ids whose last outcome was an error are left out so they are tried again.
func Done(dir string) (map[int]bool, error) {
	f, err := os.Open(filepath.Join(dir, "index.tsv"))
	if os.IsNotExist(err) {
		return map[int]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	done := map[int]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Split(sc.Text(), "\t")
		id, err := strconv.Atoi(fs[0])
		if err != nil || len(fs) < 2 {
			continue // header or a torn last line
		}
		done[id] = fs[1] != Error
	}
	return done, sc.Err()
}

// Fetcher downloads files politely: a few workers sharing one minimum gap
// between requests, a gap that doubles on errors (up to MaxGap) and resets on
// success, and a pause while PauseWhile reports another crawl is running.
type Fetcher struct {
	Base       string // e.g. https://legallaw.ul.edu.lb
	Dir        string
	Workers    int
	Gap        time.Duration
	MaxGap     time.Duration
	Timeout    time.Duration
	UserAgent  string
	PauseWhile func() bool
	Log        func(format string, args ...any)

	client *http.Client
	mu     sync.Mutex
	next   time.Time
	gap    time.Duration
	index  *os.File
}

// Run fetches every id not yet settled. It returns when all are tried or ctx ends.
func (f *Fetcher) Run(ctx context.Context, ids []int) error {
	done, err := Done(f.Dir)
	if err != nil {
		return err
	}
	var todo []int
	for _, id := range ids {
		if !done[id] {
			todo = append(todo, id)
		}
	}
	f.Log("ruling files: %d linked, %d settled, %d to fetch", len(ids), len(ids)-len(todo), len(todo))
	if err := os.MkdirAll(f.Dir, 0o755); err != nil {
		return err
	}
	idx, err := os.OpenFile(filepath.Join(f.Dir, "index.tsv"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer idx.Close()
	if st, _ := idx.Stat(); st.Size() == 0 {
		fmt.Fprintln(idx, "id\toutcome\tbytes\tsha256\tfetched_at\tnote")
	}
	f.index, f.gap = idx, f.Gap
	f.client = &http.Client{Timeout: f.Timeout}

	jobs := make(chan int)
	var wg sync.WaitGroup
	var n, ok, none int
	start := time.Now()
	for w := 0; w < f.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				out := f.one(ctx, id)
				f.mu.Lock()
				n++
				switch out {
				case OK:
					ok++
				case None:
					none++
				}
				if n%500 == 0 {
					f.Log("%d/%d fetched (%d pdf, %d no file), %.1f/min, gap %s",
						n, len(todo), ok, none, float64(n)/time.Since(start).Minutes(), f.gap)
				}
				f.mu.Unlock()
			}
		}()
	}
	for _, id := range todo {
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
	f.Log("ruling files: done %d (%d pdf, %d no file) in %s", n, ok, none, time.Since(start).Round(time.Second))
	return ctx.Err()
}

// wait reserves the next request slot. The cursor never runs more than one
// gap ahead of now, so an error burst cannot push the schedule far into the
// future and stall every worker.
func (f *Fetcher) wait(ctx context.Context) {
	for f.PauseWhile != nil && f.PauseWhile() {
		f.Log("paused: another crawl is running")
		select {
		case <-time.After(time.Minute):
		case <-ctx.Done():
			return
		}
	}
	f.mu.Lock()
	now := time.Now()
	if f.next.Before(now) {
		f.next = now
	}
	at := f.next
	f.next = f.next.Add(f.gap)
	f.mu.Unlock()
	select {
	case <-time.After(time.Until(at)):
	case <-ctx.Done():
	}
}

func (f *Fetcher) adjust(failed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if failed {
		f.gap = min(f.gap*2, f.MaxGap)
	} else {
		f.gap = f.Gap
	}
}

func (f *Fetcher) one(ctx context.Context, id int) string {
	f.wait(ctx)
	if ctx.Err() != nil {
		return ""
	}
	body, err := f.get(ctx, id)
	if err != nil {
		f.adjust(true)
		f.record(id, Error, nil, err.Error())
		return Error
	}
	f.adjust(false)
	out := Classify(body)
	switch out {
	case OK:
		err = writeFile(Path(f.Dir, id), body)
	case Other:
		err = writeFile(strings.TrimSuffix(Path(f.Dir, id), ".pdf")+".html", body)
	}
	if err != nil {
		f.record(id, Error, nil, err.Error())
		return Error
	}
	f.record(id, out, body, "")
	return out
}

func (f *Fetcher) get(ctx context.Context, id int) ([]byte, error) {
	url := fmt.Sprintf("%s/RulingFile.aspx?RuliID=%d&type=list", f.Base, id)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return body, nil
}

func (f *Fetcher) record(id int, out string, body []byte, note string) {
	sum := ""
	if body != nil {
		h := sha256.Sum256(body)
		sum = hex.EncodeToString(h[:])
	}
	note = strings.NewReplacer("\t", " ", "\n", " ").Replace(note)
	f.mu.Lock()
	defer f.mu.Unlock()
	fmt.Fprintf(f.index, "%d\t%s\t%d\t%s\t%s\t%s\n", id, out, len(body), sum, time.Now().UTC().Format(time.RFC3339), note)
}

// writeFile writes through a temporary name so a killed run never leaves half a PDF.
func writeFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".part"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Entry is one row of index.tsv.
type Entry struct {
	ID        int
	Outcome   string
	Bytes     int
	SHA256    string
	FetchedAt time.Time
	Note      string
}

// Entries reads index.tsv and returns each id's last row (the file is
// append-only, so the last one is the current outcome). A missing index is an
// empty result; the header and torn lines are skipped.
func Entries(dir string) (map[int]Entry, error) {
	f, err := os.Open(filepath.Join(dir, "index.tsv"))
	if os.IsNotExist(err) {
		return map[int]Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[int]Entry{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Split(sc.Text(), "\t")
		if len(fs) < 5 {
			continue
		}
		id, err := strconv.Atoi(fs[0])
		if err != nil {
			continue
		}
		e := Entry{ID: id, Outcome: fs[1], SHA256: fs[3]}
		e.Bytes, _ = strconv.Atoi(fs[2])
		e.FetchedAt, _ = time.Parse(time.RFC3339, fs[4])
		if len(fs) > 5 {
			e.Note = fs[5]
		}
		out[id] = e
	}
	return out, sc.Err()
}
