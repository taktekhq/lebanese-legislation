package site

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var OfficialDir string

const (
	mkStart = ""
	mkSep   = ""
	mkEnd   = ""
)

type OSeg struct {
	Text string
	Alt  string
	Flag bool
}

type OPara []OSeg

type OPage struct {
	N    int
	Img  string
	Cols [][]OPara
}

type Official struct {
	ID                  int
	Kind, Number, Title string
	IssuedOn, IssueDate *time.Time
	Issue               string
	PageFrom, PageTo    int
	PCMURL              string
	SourceName          string
	SourceURL           string
	PDF                 string
	Pages               []OPage
	Words, Flagged      int
	Agreement           float64
	L                   map[string]string
}

func parseCol(col string) []OPara {
	var out []OPara
	for _, ln := range strings.Split(col, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var p OPara
		for ln != "" {
			i := strings.Index(ln, mkStart)
			if i < 0 {
				p = append(p, OSeg{Text: ln})
				break
			}
			if i > 0 {
				p = append(p, OSeg{Text: strings.TrimSpace(ln[:i]) + " "})
			}
			ln = ln[i+len(mkStart):]
			j := strings.Index(ln, mkEnd)
			if j < 0 {
				p = append(p, OSeg{Text: ln})
				break
			}
			a, b, _ := strings.Cut(ln[:j], mkSep)
			p = append(p, OSeg{Text: a, Alt: b, Flag: true})
			ln = strings.TrimLeft(ln[j+len(mkEnd):], " ")
			if ln != "" {
				p = append(p, OSeg{Text: " "})
			}
		}
		out = append(out, p)
	}
	return out
}

func (s *Server) loadOfficial(r *http.Request, id int) (*Official, int, error) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	var o Official
	var body []byte
	var superseded *int
	var num, issue, pcm, srcURL *string
	var pf, pt *int
	err := s.db.QueryRow(ctx, `SELECT kind, number, title, issued_on, gazette_issue, gazette_issue_date, gazette_page_from, gazette_page_to,
		pcm_url, source_name, source_url, words, flagged_spans, agreement, body, superseded_by_law_id FROM official_text WHERE id = $1`, id).
		Scan(&o.Kind, &num, &o.Title, &o.IssuedOn, &issue, &o.IssueDate, &pf, &pt, &pcm, &o.SourceName, &srcURL, &o.Words, &o.Flagged, &o.Agreement, &body, &superseded)
	if err != nil {
		return nil, 0, err
	}
	if superseded != nil {
		return nil, *superseded, nil
	}
	o.ID, o.Number, o.Issue, o.PCMURL, o.SourceURL = id, str(num), str(issue), str(pcm), str(srcURL)
	if pf != nil {
		o.PageFrom = *pf
	}
	if pt != nil {
		o.PageTo = *pt
	}
	var b struct {
		Pages []struct {
			N    int      `json:"n"`
			Cols []string `json:"cols"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, 0, err
	}
	for _, p := range b.Pages {
		op := OPage{N: p.N}
		if OfficialDir != "" {
			if _, err := os.Stat(filepath.Join(OfficialDir, strconv.Itoa(id), fmt.Sprintf("p%d.png", p.N))); err == nil {
				op.Img = fmt.Sprintf("/official-files/%d/p%d.png", id, p.N)
			}
		}
		for _, c := range p.Cols {
			op.Cols = append(op.Cols, parseCol(c))
		}
		o.Pages = append(o.Pages, op)
	}
	if OfficialDir != "" {
		if _, err := os.Stat(filepath.Join(OfficialDir, strconv.Itoa(id), "source.pdf")); err == nil {
			o.PDF = fmt.Sprintf("/official-files/%d/source.pdf", id)
		}
	}
	return &o, 0, nil
}

func (s *Server) official(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	o, law, err := s.loadOfficial(r, id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if o == nil {
		http.Redirect(w, r, s.u(r, s.finalPath(r.Context(), fmt.Sprintf("/law/%d/", law))), http.StatusMovedPermanently)
		return
	}
	o.L = map[string]string{
		"back":     s.trOr(r, "Latest texts", "official.back"),
		"notice":   "نص منقول عن صفحات الجريدة الرسمية الممسوحة ضوئياً، وليس النسخة الرسمية. الجريدة الرسمية هي المرجع القانوني، والنسخ المصدّقة تُطلب من إدارة الجريدة الرسمية. / An unofficial transcription of the Official Gazette's page scans. The Gazette is the legal source; certified copies come from the Gazette office.",
		"source":   s.trOr(r, "Page scans of the Official Gazette, as published by", "official.source"),
		"images":   s.trOr(r, "The official pages", "official.images"),
		"text":     s.trOr(r, "The text read from these pages", "official.text"),
		"flagged":  s.trOr(r, "Words marked like this were read differently by the two readers: check them against the page image. The first reading is shown; hover for the second.", "official.flagged"),
		"pdf":      s.trOr(r, "Download the page scans (PDF)", "official.pdf"),
		"pcm":      s.trOr(r, "The Official Gazette page on the Presidency of the Council of Ministers site", "official.pcm"),
		"issue":    s.trOr(r, "Official Gazette", "official.issue"),
		"page":     s.trOr(r, "page", "official.page"),
		"pageword": s.trOr(r, "Page", "official.pageword"),
		"kind":     s.trOr(r, "Type", "official.kind"),
		"number":   s.trOr(r, "Number", "official.number"),
		"date":     s.trOr(r, "Date", "official.date"),
		"from":     s.trOr(r, "Source", "official.from"),
		"reading":  s.trOr(r, "Reading", "official.reading"),
	}
	title := fmt.Sprintf("%s %s", o.Kind, o.Number)
	if o.Number == "" {
		title = o.Kind
	}
	desc := s.trOr(r, fmt.Sprintf("%s: %s. Transcribed from the Official Gazette's page scans, with the page images beside the text.", title, o.Title), "official.desc", title, o.Title)
	s.render(w, r, "official", 200, Page{Lang: "ar", Title: s.brandTitle(r, title+" "+cut(o.Title, 60)), Desc: desc,
		Path: fmt.Sprintf("/official/%d/", id), OGType: "article", Data: o})
}

type OfficialRow struct {
	ID                  int
	Kind, Number, Title string
	IssuedOn            *time.Time
	Issue               string
}

type OfficialList struct {
	Rows    []OfficialRow
	Total   int
	Updated *Updated
	L       map[string]string
}

func (s *Server) officialList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	rows, err := s.db.Query(ctx, `SELECT id, kind, number, title, issued_on, gazette_issue FROM official_text
		WHERE superseded_by_law_id IS NULL ORDER BY issued_on DESC NULLS LAST, number_int DESC NULLS LAST, id DESC LIMIT 500`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	l := OfficialList{L: map[string]string{
		"title": s.trOr(r, "Latest texts from the Official Gazette", "official.list_title"),
		"intro": s.trOr(r, "Laws and decrees newer than the Lebanese University archive, transcribed from the Official Gazette's page scans. When the University archive adds a text, its page replaces ours.", "official.list_intro"),
	}}
	for rows.Next() {
		var x OfficialRow
		var num, issue *string
		if err := rows.Scan(&x.ID, &x.Kind, &num, &x.Title, &x.IssuedOn, &issue); err != nil {
			s.fail(w, r, err)
			return
		}
		x.Number, x.Issue = str(num), str(issue)
		l.Rows = append(l.Rows, x)
	}
	l.Total = len(l.Rows)
	l.Updated = s.updated(r, "official")
	s.render(w, r, "official_list", 200, Page{Lang: "ar", Title: s.brandTitle(r, l.L["title"]), Desc: l.L["intro"], Path: "/official/", Data: l})
}

func (s *Server) officialFiles(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	name := r.PathValue("name")
	if !ok || OfficialDir == "" || strings.ContainsAny(name, "/\\") || (!strings.HasSuffix(name, ".png") && name != "source.pdf") {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, filepath.Join(OfficialDir, strconv.Itoa(id), name))
}
