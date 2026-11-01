package modelout

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/rulingfile"
)

// The full judgments behind ruling pages (lebleg ruling-files downloads the
// scanned PDFs, lebleg ruling-read has Gemini transcribe them). The PDFs stay
// outside the database; what is loaded is index.tsv's row for each PDF and the
// text read from it, page by page. Both jobs run for days, so a folder is
// loaded as it stands: a ruling not read yet simply has no pages.

var (
	rulingFileCols = []string{"ruling_id", "pages", "bytes", "sha256", "fetched_at"}
	rulingPageCols = []string{"ruling_id", "n", "text_ar", "model", "read_at", "low_confidence"}
)

// rulingBatch is how many readings are held before they are written out, so
// ~45,000 files stream through in a few hundred MB at most.
const rulingBatch = 500

// RulingStats is what RulingFiles loaded.
type RulingStats struct {
	Files int // rulings whose scanned judgment (PDF) is held
	Read  int // of those, rulings with a machine reading
	Pages int // pages of text in those readings
}

// RulingFiles loads the folder `lebleg ruling-files` fills (dir) into
// ruling_file and ruling_page: each held ruling's PDF row, and the text a
// model read from it, page by page. It starts clean, so a rerun on a loaded
// database ("-only ruling-files") holds no stale page.
func RulingFiles(ctx context.Context, conn *pgx.Conn, dir string, note func(string, ...any)) (RulingStats, error) {
	var st RulingStats
	if _, err := conn.Exec(ctx, "TRUNCATE ruling_page, ruling_file"); err != nil {
		return st, err
	}
	have := map[int]bool{}
	rows, err := conn.Query(ctx, "SELECT id FROM ruling")
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return st, err
		}
		have[id] = true
	}
	if err := rows.Err(); err != nil {
		return st, err
	}

	var batch [][]any
	read := 0
	pages, err := walkReadings(dir, have, note, func(rd *rulingfile.Reading, ps []rulingfile.Page) error {
		for _, p := range ps {
			model := p.Model // pages of one ruling can come from different readers (rulingfile/route.go)
			if model == "" {
				model = rd.Model
			}
			batch = append(batch, []any{rd.ID, p.N, p.Text, model, rd.ReadAt, p.LowConfidence})
		}
		st.Read++
		st.Pages += len(ps)
		if read++; read%rulingBatch == 0 {
			err := copyRows(ctx, conn, "ruling_page", rulingPageCols, batch)
			batch = nil
			return err
		}
		return nil
	})
	if err != nil {
		return st, err
	}
	if err := copyRows(ctx, conn, "ruling_page", rulingPageCols, batch); err != nil {
		return st, err
	}

	index, err := rulingfile.Entries(dir)
	if err != nil {
		return st, err
	}
	var files [][]any
	for _, id := range rulingFileIDs(index, have) {
		e := index[id]
		var n any
		if p, ok := pages[id]; ok {
			n = p
		}
		files = append(files, []any{id, n, e.Bytes, e.SHA256, e.FetchedAt})
	}
	st.Files = len(files)
	if n := len(index) - st.Files; n > 0 {
		note("ruling files: %d index rows are not a PDF of a loaded ruling (no file at the Centre, or an id we do not hold)", n)
	}
	if st.Files == 0 {
		return st, nil
	}
	if _, err := conn.CopyFrom(ctx, pgx.Identifier{"ruling_file"}, rulingFileCols, pgx.CopyFromRows(files)); err != nil {
		return st, fmt.Errorf("copy ruling_file: %w", err)
	}
	return st, nil
}

// rulingFileIDs lists, in id order, the rulings that have a downloaded PDF:
// the index's last row for the id says "ok" and the ruling is one we hold.
func rulingFileIDs(index map[int]rulingfile.Entry, have map[int]bool) []int {
	var ids []int
	for id, e := range index {
		if e.Outcome == rulingfile.OK && have[id] {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

// walkReadings hands fn every reading in dir (<id÷1000>/<id>.read.json) that
// belongs to a ruling in have, with the pages that have text (a page the
// reader skipped, or an empty one, is left out). It returns each such
// ruling's page count in the scan. Unreadable or foreign files are noted and
// skipped: the folder is written by a job that is still running.
func walkReadings(dir string, have map[int]bool, note func(string, ...any),
	fn func(*rulingfile.Reading, []rulingfile.Page) error) (map[int]int, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*", "*.read.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	counts := map[int]int{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var rd rulingfile.Reading
		if err := json.Unmarshal(raw, &rd); err != nil {
			note("ruling reading %s skipped: %v", filepath.Base(f), err)
			continue
		}
		if !have[rd.ID] {
			note("ruling reading %d skipped: no such ruling", rd.ID)
			continue
		}
		var ps []rulingfile.Page
		for _, p := range rd.Pages {
			if !p.Missing && p.N > 0 && strings.TrimSpace(p.Text) != "" {
				ps = append(ps, p)
			}
		}
		if len(ps) == 0 {
			continue
		}
		counts[rd.ID] = len(rd.Pages)
		if err := fn(&rd, ps); err != nil {
			return nil, err
		}
	}
	return counts, nil
}
