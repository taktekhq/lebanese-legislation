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

	"path/filepath"
)

func (s *Server) SetRulingFiles(dir string) { s.rulingDir = dir }

var rulingFileName = regexp.MustCompile(`^([1-9][0-9]{0,8})\.pdf$`)

func (s *Server) rulingFile(w http.ResponseWriter, r *http.Request) {
	m := rulingFileName.FindStringSubmatch(r.PathValue("name"))
	if m == nil || s.rulingDir == "" {
		s.notFound(w, r)
		return
	}
	id, _ := strconv.Atoi(m[1])
	path := filepath.Join(s.rulingDir, fmt.Sprintf("%04d", id/1000), strconv.Itoa(id)+".pdf")
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")

	w.Header().Set("X-Robots-Tag", "noindex")
	http.ServeFile(w, r, path)
}

type DocFile struct {
	URL   string
	Label string
	Size  string
}

func ULRulingPDF(id int) string {
	return fmt.Sprintf("https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=%d&type=list", id)
}

type DocReading struct {
	Heading string
	Notice  string

	MarksHelp template.HTML
	Pages     []DocPage
}

const lowConfidenceEN = "Low-confidence reading: this page is hard to read (mostly handwriting), " +
	"so expect errors. The scan is the reference."

const marksHelpEN = "Highlighted words, like {example}, are places where the machine readings disagree: " +
	"both readings are shown, separated by a slash, and neither is chosen. Check those words against the scan."

func marksHelp(text string) template.HTML {
	before, after, _ := strings.Cut(text, "{example}")
	sample := marks("\uE000" + "قراءة أولى" + "\uE001" + "قراءة ثانية" + "\uE002")
	return template.HTML(template.HTMLEscapeString(before)) + `<span lang="ar" dir="rtl">` + sample + `</span>` +
		template.HTML(template.HTMLEscapeString(after))
}

type DocPage struct {
	N     int
	Label string
	Text  string
	Paras []string

	LowConfidence bool
	Warn          string
}

type judgment struct {
	HasFile bool
	Bytes   int
	Scanned int
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
	if undefinedColumn(err) {
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
		j.Model = strings.Join(models, ",")
		p.Paras = paras(&p.Text)
		j.Pages = append(j.Pages, p)
	}
	return j, rows.Err()
}

func kb(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", max(1, (n+512)/1024))
}

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
			d.Note = ""
		case j.HasFile:
			d.Note = s.trOr(r, "The Centre holds this judgment as a scanned file only. Open the scan above; its text has not been read yet.", "ruling.scan_only")
		}
	}
}

func (r *DocReading) firstText() string {
	if r == nil {
		return ""
	}

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

func undefinedColumn(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "42703"
}
