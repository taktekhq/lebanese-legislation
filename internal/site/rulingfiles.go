package site

import (
	"errors"

	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"html/template"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/rulingfile"
)

// The full judgment behind a ruling page: the Centre's scanned PDF, kept in a
// folder outside the database (cmd/ruling-files), and its machine reading
// in ruling_page (cmd/ruling-read). See internal/modelout/rulings.go.

// SetRulingFiles names the folder holding the judgment PDFs, laid out as
// cmd/ruling-files writes it (<id÷1000>/<id>.pdf). Empty: no PDF is served.
func (s *Server) SetRulingFiles(dir string) { s.rulingDir = dir }

var rulingFileName = regexp.MustCompile(`^([1-9][0-9]{0,8})\.pdf$`)

// rulingFile serves a ruling's scanned judgment at /ruling-files/<id>.pdf.
// Only the id is taken from the request; the path is built from it.
func (s *Server) rulingFile(w http.ResponseWriter, r *http.Request) {
	m := rulingFileName.FindStringSubmatch(r.PathValue("name"))
	if m == nil || s.rulingDir == "" {
		s.notFound(w, r)
		return
	}
	id, _ := strconv.Atoi(m[1])
	path := rulingfile.Path(s.rulingDir, id)
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	// A scan with no text layer: nothing for a search engine to read, and a
	// heavy download to be crawled 45,000 times. The ruling's page is indexed.
	w.Header().Set("X-Robots-Tag", "noindex")
	http.ServeFile(w, r, path)
}

// DocFile is the scanned judgment behind a ruling page.
// The link goes to the scan at the source, the Lebanese University's site,
// not to our copy: one button, the original.
type DocFile struct {
	URL   string
	Label string
	Size  string // "175 KB"
}

// ULRulingPDF is the judgment's scan on the Lebanese University's site,
// served as a PDF straight from that address.
func ULRulingPDF(id int) string {
	return fmt.Sprintf("https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=%d&type=list", id)
}

// DocReading is the machine reading of the scan, page by page, under a notice
// that it is not the judgment itself.
type DocReading struct {
	Heading string
	Notice  string
	// MarksHelp explains the highlights, with a sample one, on a reading
	// that has spans the readers could not settle; empty otherwise.
	MarksHelp template.HTML
	Pages     []DocPage
}

// marksHelpEN is the English explanation; {example} becomes a sample highlight.
const lowConfidenceEN = "Low-confidence reading: this page is hard to read (mostly handwriting), " +
	"so expect errors. The scan is the reference."

const marksHelpEN = "Highlighted words, like {example}, are places where the machine readings disagree: " +
	"both readings are shown, separated by a slash, and neither is chosen. Check those words against the scan."

// marksHelp renders the explanation with a highlight that looks exactly like
// the ones in the reading below it.
func marksHelp(text string) template.HTML {
	before, after, _ := strings.Cut(text, "{example}")
	sample := marks("\uE000" + "قراءة أولى" + "\uE001" + "قراءة ثانية" + "\uE002")
	return template.HTML(template.HTMLEscapeString(before)) + `<span lang="ar" dir="rtl">` + sample + `</span>` +
		template.HTML(template.HTMLEscapeString(after))
}

// DocPage is one page of the reading.
type DocPage struct {
	N     int
	Label string
	Text  string   // the reading as written
	Paras []string // blocks separated by a blank line; line breaks inside are the scan's
	// LowConfidence: hard to read (mostly handwriting), from ruling_page.
	LowConfidence bool
	Warn          string // the page's warning, when LowConfidence
}

// judgment is what the database holds about a ruling's scan.
type judgment struct {
	HasFile bool
	Bytes   int
	Scanned int // pages in the scan, 0 when unknown
	Model   string
	Pages   []DocPage
}

func (s *Server) loadJudgment(ctx context.Context, id int) (judgment, error) {
	var j judgment
	var scanned *int
	err := s.db.QueryRow(ctx, `SELECT bytes, pages FROM ruling_file WHERE ruling_id = $1`, id).Scan(&j.Bytes, &scanned)
	switch {
	case err == nil:
		j.HasFile = true
		if scanned != nil {
			j.Scanned = *scanned
		}
	case err != pgx.ErrNoRows:
		return j, err
	}
	rows, err := s.db.Query(ctx, `SELECT n, text_ar, model, low_confidence FROM ruling_page WHERE ruling_id = $1 ORDER BY n`, id)
	if undefinedColumn(err) { // a database loaded before low_confidence: the pages without their warnings beats a 500
		rows, err = s.db.Query(ctx, `SELECT n, text_ar, model, false FROM ruling_page WHERE ruling_id = $1 ORDER BY n`, id)
	}
	if err != nil {
		return j, err
	}
	defer rows.Close()
	var models []string
	for rows.Next() {
		var p DocPage
		var model string
		if err := rows.Scan(&p.N, &p.Text, &model, &p.LowConfidence); err != nil {
			return j, err
		}
		if !slices.Contains(models, model) {
			models = append(models, model)
		}
		j.Model = strings.Join(models, ",") // for the API and the dataset, never shown on the page
		p.Paras = paras(&p.Text)
		j.Pages = append(j.Pages, p)
	}
	return j, rows.Err()
}

// kb says a file's size the way a download link does.
func kb(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", max(1, (n+512)/1024))
}

// judgmentDoc fills a ruling page's file link and reading from j, in the
// request's language. With a file but no reading the page says the scan is
// the only source; without either, the Centre's file-only note stays.
func (s *Server) judgmentDoc(r *http.Request, d *Doc, id int, j judgment) {
	if j.HasFile {
		d.File = &DocFile{URL: ULRulingPDF(id), Size: kb(j.Bytes),
			Label: s.trOr(r, "Full judgment (scan, PDF)", "ruling.file_link")}
	}
	if len(j.Pages) > 0 {
		page := s.trOr(r, "Page", "ruling.page")
		for i := range j.Pages {
			j.Pages[i].Label = page + " " + strconv.Itoa(j.Pages[i].N)
			if j.Pages[i].LowConfidence {
				j.Pages[i].Warn = s.trOr(r, lowConfidenceEN, "ruling.reading_low")
			}
		}
		d.Reading = &DocReading{Pages: j.Pages,
			Heading: s.trOr(r, "The full judgment", "ruling.reading_title"),
			Notice: s.trOr(r, "Machine reading of the scanned judgment. It may contain errors; the scan is the reference.",
				"ruling.reading_notice")}
		for _, p := range j.Pages {
			if strings.ContainsRune(p.Text, '\uE000') {
				d.Reading.MarksHelp = marksHelp(s.trOr(r, marksHelpEN, "ruling.reading_marks"))
				break
			}
		}
	}
	if len(d.Paras) == 0 {
		switch {
		case d.Reading != nil:
			d.Note = "" // the file-only note no longer applies: the text is below
		case j.HasFile:
			d.Note = s.trOr(r, "The Centre holds this judgment as a scanned file only. Open the scan above; its text has not been read yet.", "ruling.scan_only")
		}
	}
}

// firstText is the start of the first page that has any text, for a page's
// description when the Centre gave no summary. A span the readers dispute
// is written ⟦one | two⟧, as the API does: plain text has no marks.
func (r *DocReading) firstText() string {
	if r == nil {
		return ""
	}
	// A trusted page first: snippets elsewhere (search results, link
	// previews) don't carry the page's warning. Low-confidence text only if that is all.
	for _, checked := range []bool{true, false} {
		for _, p := range r.Pages {
			if p.LowConfidence == checked {
				continue
			}
			if t := strings.Join(strings.Fields(scanReadings(strings.Join(p.Paras, " "))), " "); t != "" {
				return t
			}
		}
	}
	return ""
}

// undefinedColumn says a query failed on a column the database doesn't have
// yet (Postgres 42703): the app can ship before the nightly load that adds it.
func undefinedColumn(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "42703"
}

// undefinedTable: the query names a table the database does not have.
func undefinedTable(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "42P01"
}
