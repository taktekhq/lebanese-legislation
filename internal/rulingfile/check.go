package rulingfile

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/taktekhq/lebanese-legislation/internal/archive"
)

var rulingLink = regexp.MustCompile(`RulingFile\.aspx\?RuliID=([0-9]+)`)

type CheckFailure struct{ Count int }

func (e *CheckFailure) Error() string { return fmt.Sprintf("check-rulings: %d failure(s)", e.Count) }

type CheckOptions struct {
	Dir    string
	PDFDir string // where the <id>.pdf files are; empty means Dir (the nightly's copy holds no PDFs)
	Since  time.Duration
	Budget float64
	Now    time.Time
}

// Check validates ruling files, recent readings, published marks and API spend.
// It reports every defect as a FAIL line; input and database errors are returned.
func Check(ctx context.Context, db interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, archiveDir string, o CheckOptions, out io.Writer) error {
	if o.Dir == "" || o.Since <= 0 || o.Budget < 0 {
		return fmt.Errorf("check-rulings: invalid options")
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	var failures atomic.Int32
	fail := func(rule string, id, page int, detail string) {
		failures.Add(1)
		_, _ = fmt.Fprintf(out, "FAIL %s ruling=%d page=%d %s\n", rule, id, page, detail)
	}
	links := map[int]bool{}
	idx, err := (archive.Archive{Dir: archiveDir}).Index("ruling")
	if err != nil {
		return err
	}
	for _, entry := range idx {
		b, e := os.ReadFile(filepath.Join(archiveDir, "ruling", fmt.Sprintf("%04d", entry.ID/1000), strconv.Itoa(entry.ID)+".html"))
		if e != nil {
			return e
		}
		match := rulingLink.FindSubmatch(b)
		if len(match) == 2 {
			linkedID, parseErr := strconv.Atoi(string(match[1]))
			links[entry.ID] = parseErr == nil && linkedID == entry.ID
		}
	}
	if len(idx) == 0 {
		return fmt.Errorf("check-rulings: no archived ruling pages under %s", archiveDir)
	}

	dbFiles := map[int]bool{}
	dbRows, err := db.Query(ctx, `SELECT f.ruling_id, (r.id IS NOT NULL) FROM ruling_file f LEFT JOIN ruling r ON r.id=f.ruling_id`)
	if err != nil {
		return err
	}
	for dbRows.Next() {
		var id int
		var exists bool
		if err := dbRows.Scan(&id, &exists); err != nil {
			dbRows.Close()
			return err
		}
		dbFiles[id] = true
		if !exists {
			fail("file-link", id, 0, "ruling row missing")
		}
	}
	if err := dbRows.Err(); err != nil {
		dbRows.Close()
		return err
	}
	dbRows.Close()
	pdfIDs := map[int]bool{}
	pdfDir := o.PDFDir
	if pdfDir == "" {
		pdfDir = o.Dir
	}
	err = filepath.WalkDir(pdfDir, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".pdf") {
			return nil
		}
		id, e := strconv.Atoi(strings.TrimSuffix(d.Name(), ".pdf"))
		if e != nil {
			return nil
		}
		pdfIDs[id] = true
		if !dbFiles[id] {
			fail("file-link", id, 0, "PDF has no ruling_file row")
		}
		if !links[id] {
			fail("file-link", id, 0, "PDF ruling has no source PDF link")
		}
		return nil
	})
	if err != nil {
		return err
	}
	for id := range dbFiles {
		if !pdfIDs[id] {
			fail("file-link", id, 0, "ruling_file row has no PDF")
		}
		if !links[id] {
			fail("file-link", id, 0, "ruling_file ruling has no source PDF link")
		}
	}

	cutoff := o.Now.Add(-o.Since)
	published := map[[2]int]string{}
	pages, err := db.Query(ctx, `SELECT ruling_id,n,text_ar,read_at FROM ruling_page`)
	if err != nil {
		return err
	}
	for pages.Next() {
		var id, n int
		var text string
		var ignoredReadAt time.Time
		if e := pages.Scan(&id, &n, &text, &ignoredReadAt); e != nil {
			pages.Close()
			return e
		}
		published[[2]int{id, n}] = text
	}
	if err := pages.Err(); err != nil {
		pages.Close()
		return err
	}
	pages.Close()

	readings, err := filepath.Glob(filepath.Join(o.Dir, "*", "*.read.json"))
	if err != nil {
		return err
	}
	sort.Strings(readings)
	for _, name := range readings {
		b, e := os.ReadFile(name)
		if e != nil {
			return e
		}
		var rd Reading
		if e = json.Unmarshal(b, &rd); e != nil {
			return fmt.Errorf("check-rulings: unreadable %s: %w", name, e)
		}
		if rd.ID <= 0 {
			return fmt.Errorf("check-rulings: invalid ruling id in %s", name)
		}
		if rd.ReadAt.IsZero() || rd.ReadAt.Before(cutoff) || rd.ReadAt.After(o.Now.Add(time.Minute)) {
			continue
		}
		seenPages := make(map[int]bool, len(rd.Pages))
		for _, p := range rd.Pages {
			if p.N < 1 || seenPages[p.N] {
				return fmt.Errorf("check-rulings: invalid or duplicate page in %s", name)
			}
			seenPages[p.N] = true
			if len(p.Want) > 0 {
				if _, ok := published[[2]int{rd.ID, p.N}]; ok {
					fail("reading-count", rd.ID, p.N, "published page still wants readers")
				} else {
					_, _ = fmt.Fprintf(out, "NOTE pending ruling=%d page=%d\n", rd.ID, p.N)
				}
				continue
			}
			if p.Missing {
				continue
			}
			if strings.TrimSpace(p.Text) == "" {
				fail("reading-count", rd.ID, p.N, "page has fewer than two readings")
				continue
			}
			model := p.Model
			if model == "" {
				model = rd.Model
			}
			voted := strings.Contains(model, "+")
			if !voted && !p.LowConfidence {
				fail("reading-count", rd.ID, p.N, "fewer than two readers")
			}
			if !voted && p.LowConfidence {
				_, _ = fmt.Fprintf(out, "NOTE single-reading low-confidence ruling=%d page=%d\n", rd.ID, p.N)
			}
			if !voted {
				continue
			}
			count, valid := markedSpans(p.Text)
			if !valid || count != p.Disputed || (p.Disputed > 0 && count == 0) {
				fail("disputed-marks", rd.ID, p.N, fmt.Sprintf("reading has %d marked spans, disputed=%d", count, p.Disputed))
			}
			if text, ok := published[[2]int{rd.ID, p.N}]; ok {
				c, v := markedSpans(text)
				if !v || c != p.Disputed {
					fail("disputed-marks", rd.ID, p.N, "published text does not preserve disputed marks")
				}
			}
		}
	}
	spent, invalid, err := ledgerSpend(filepath.Join(o.Dir, "gemini", "spend.tsv"), false, out)
	if err != nil {
		return err
	}
	if invalid {
		failures.Add(1)
	}
	claude, invalid, err := ledgerSpend(filepath.Join(o.Dir, "claude-api", "spend.tsv"), true, out)
	if err != nil {
		return err
	}
	if invalid {
		failures.Add(1)
	}
	spent += claude
	if spent > o.Budget {
		fail("budget", 0, 0, fmt.Sprintf("spend $%.6f exceeds $%.2f", spent, o.Budget))
	} else if spent >= o.Budget*.9 {
		_, _ = fmt.Fprintf(out, "WARN ruling ledger spend $%.6f is at least 90%% of $%.2f\n", spent, o.Budget)
	}
	if failures.Load() > 0 {
		return &CheckFailure{Count: int(failures.Load())}
	}
	return nil
}

func markedSpans(s string) (int, bool) {
	count := 0
	for len(s) > 0 {
		i := strings.Index(s, markStart)
		if i < 0 {
			return count, !strings.ContainsAny(s, markSep+markEnd)
		}
		s = s[i+len(markStart):]
		j := strings.Index(s, markSep)
		if j <= 0 {
			return count, false
		}
		a := strings.TrimSpace(s[:j])
		s = s[j+len(markSep):]
		k := strings.Index(s, markEnd)
		if k < 0 {
			return count, false
		}
		b := strings.TrimSpace(s[:k])
		if b == "" || a == b {
			return count, false
		}
		count++
		s = s[k+len(markEnd):]
	}
	return count, true
}

func ledgerSpend(path string, claude bool, out io.Writer) (float64, bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		_, _ = fmt.Fprintf(out, "FAIL ledger ruling=0 page=0 %s missing\n", filepath.Base(path))
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	total := 0.0
	invalid := false
	line := 0
	for sc.Scan() {
		line++
		cols := strings.Split(sc.Text(), "\t")
		ix := 2
		if claude {
			ix = 3
		}
		if line == 1 && (cols[0] == "batch" || strings.EqualFold(cols[0], "batch_id")) {
			continue
		}
		if len(cols) <= ix {
			invalid = true
			_, _ = fmt.Fprintf(out, "FAIL ledger ruling=0 page=0 %s:%d malformed row\n", filepath.Base(path), line)
			continue
		}
		v, e := strconv.ParseFloat(cols[ix], 64)
		if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			invalid = true
			_, _ = fmt.Fprintf(out, "FAIL ledger ruling=0 page=0 %s:%d malformed amount\n", filepath.Base(path), line)
			continue
		}
		total += v
	}
	if err := sc.Err(); err != nil {
		return 0, false, err
	}
	return total, invalid, nil
}
