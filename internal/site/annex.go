package site

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"time"
)

// What we read ourselves from the Official Gazette's page images where the
// Lebanese University's text lacks it (gazette_annex, data/gazette_annex.sql):
// the tables annexed to a text, shown after its articles (Law 243/2012's
// violation tables), and an article's wording as first published, folded
// under the article (the Social Security Law's Articles 49 to 54 in 1963).
// Machine transcription by two Gemini readings; where they differed, a Gemini
// model decided from the page, and those rows say so. Nizar, 5 Nov 2026: "No
// one will [check them], just put them live. And the students will check
// whatever they are studying." The University's text stays the text: a first
// wording sits beside it, never instead of it.

// Annex is the tables annexed to a text, as read from the Gazette's pages.
type Annex struct {
	Issue   string // the Gazette issue, "45"
	Date    *time.Time
	PDF     string // the issue's PDF
	Tables  []AnnexTable
	Decided int  // rows whose reading a model decided
	Traffic bool // Law 243's tables: their fines are Article 374's 2012 ranges
	pages   []int
}

// AnnexTable is one table: a category of Law 243's violations.
type AnnexTable struct {
	Category         string // "1" to "5", "p" for pedestrians
	Title            string // as printed
	FineMin, FineMax int    // Article 374's range for the category, 2012 amounts
	First, Last      int    // its Gazette pages
	PDF              string // the PDF at its first page
	Rows             []AnnexRow
}

// Pages is the table's page range, "4970-4975".
func (t AnnexTable) Pages() string { return pageRange(t.First, t.Last) }

// AnnexRow is a row of an annexed table, Arabic as printed.
type AnnexRow struct {
	Anchor     string // annex-<category>-<serial>, the row's id on the page and in topic cites
	Serial     int
	Article    string // as printed, Arabic-Indic digits
	ArticleNum string // the same in ASCII digits, for its link; "" when not a plain number
	Extra      string // further articles, as printed ("و٢٩٨")
	Violation  string
	Points     string
	Notes      string
	ReplacedBy *AnnexLink // a later text replaced the row (Law 278/2014, Article 1)
	DecidedBy  string     // the model that decided the reading, "" when the readings agreed
	Others     []AnnexReading
}

// AnnexLink is an article of another text.
type AnnexLink struct {
	LawID   int
	Article string
	Cite    string // how the text is cited, HTML-escaped (it fills a UI string)
}

// AnnexReading is a reading of a decided field other than the one shown.
type AnnexReading struct {
	Field  string // extra_articles, violation, points, notes, article
	Value  string
	Source string // "reader" (a machine reading of the page) or "isf" (the ISF's typed copy)
}

// Original is an article's wording as first published, read from the
// Gazette's pages.
type Original struct {
	Anchor     string // original-<number>
	Text       string
	Issue      string
	Date       *time.Time
	Pages      string // "14-17"
	Supplement bool   // printed in a supplement (ملحق) of the issue
	PDF        string
}

// Year is the year it was published.
func (o *Original) Year() string {
	if o.Date == nil {
		return ""
	}
	return strconv.Itoa(o.Date.Year())
}

// PagesIn lists the annex's Gazette pages in a language: "4970-4995, 5001".
func (a *Annex) PagesIn(lang string) string {
	sep := ", "
	if lang == "ar" {
		sep = "، "
	}
	var out []string
	for i := 0; i < len(a.pages); {
		j := i
		for j+1 < len(a.pages) && a.pages[j+1] == a.pages[j]+1 {
			j++
		}
		out = append(out, pageRange(a.pages[i], a.pages[j]))
		i = j + 1
	}
	return strings.Join(out, sep)
}

func pageRange(a, b int) string {
	if a == b {
		return strconv.Itoa(a)
	}
	return fmt.Sprintf("%d-%d", a, b)
}

// plainNumber is an article number as printed in ASCII digits (calculated.go's
// asciiDigits), "" when it is not one plain number.
func plainNumber(s string) string {
	s = strings.TrimSpace(asciiDigits(s))
	if _, err := strconv.Atoi(s); err != nil {
		return ""
	}
	return s
}

// annexReadings is gazette_annex.readings: per decided field, every candidate
// reading with its source, and what the model chose.
type annexReadings map[string]struct {
	Candidates []struct {
		Source string `json:"source"`
		Value  string `json:"value"`
	} `json:"candidates"`
}

// attachGazette adds a text's annexed tables and its articles' first
// wordings. An older database has no gazette_annex: the page shows as before.
func (s *Server) attachGazette(ctx context.Context, l *Law) {
	rows, err := s.db.Query(ctx, `SELECT anchor, kind, coalesce(category, ''), coalesce(table_title, ''), coalesce(serial, 0),
		coalesce(article, ''), coalesce(extra_articles, ''), coalesce(violation, ''), coalesce(points, ''), coalesce(notes, ''),
		coalesce(fine_min, 0), coalesce(fine_max, 0), coalesce(number, ''), coalesce(text_ar, ''), gazette_issue, gazette_date,
		gazette_pages, source_url, replaced_by_law_id, coalesce(replaced_by_article, ''), coalesce(decided_by, ''), readings
		FROM gazette_annex WHERE law_id = $1 ORDER BY kind DESC, category, serial, number`, l.ID)
	if err != nil {
		return
	}
	defer rows.Close()
	originals := map[string]*Original{}
	var a *Annex
	for rows.Next() {
		var anchor, kind, category, title, article, extra, violation, points, notes, number, text, issue, pages, src, replArt, decided string
		var serial int
		var fineMin, fineMax int64
		var date time.Time
		var replLaw *int
		var readings []byte
		if rows.Scan(&anchor, &kind, &category, &title, &serial, &article, &extra, &violation, &points, &notes, &fineMin, &fineMax,
			&number, &text, &issue, &date, &pages, &src, &replLaw, &replArt, &decided, &readings) != nil {
			return
		}
		pdf, _, _ := strings.Cut(src, "#")
		if kind == "article_original" {
			p, supp := strings.CutSuffix(pages, " (supplement)")
			originals[number] = &Original{Anchor: anchor, Text: text, Issue: issue, Date: &date, Pages: p, Supplement: supp, PDF: pdf}
			continue
		}
		if a == nil {
			a = &Annex{Issue: issue, Date: &date, PDF: pdf, Traffic: l.ID == 246455}
		}
		page, _ := strconv.Atoi(pages)
		if n := len(a.Tables); n == 0 || a.Tables[n-1].Category != category {
			a.Tables = append(a.Tables, AnnexTable{Category: category, Title: title, FineMin: int(fineMin), FineMax: int(fineMax),
				First: page, Last: page, PDF: src})
		}
		t := &a.Tables[len(a.Tables)-1]
		t.First, t.Last = min(t.First, page), max(t.Last, page)
		a.pages = append(a.pages, page)
		row := AnnexRow{Anchor: anchor, Serial: serial, Article: article, ArticleNum: plainNumber(article), Extra: extra,
			Violation: violation, Points: points, Notes: notes, DecidedBy: decided}
		if replLaw != nil {
			row.ReplacedBy = &AnnexLink{LawID: *replLaw, Article: replArt}
		}
		if decided != "" {
			a.Decided++
			row.Others = otherReadings(readings, map[string]string{"article": article, "extra_articles": extra,
				"violation": violation, "points": points, "notes": notes}, row.ReplacedBy != nil)
		}
		t.Rows = append(t.Rows, row)
	}
	if rows.Err() != nil {
		return
	}
	rows.Close() // before the replacing texts' names are read
	if a != nil {
		sort.Ints(a.pages)
		a.pages = uniqInts(a.pages)
		for i := range a.Tables {
			for _, row := range a.Tables[i].Rows {
				if r := row.ReplacedBy; r != nil {
					r.Cite = template.HTMLEscapeString(s.topicLaw(ctx, r.LawID).cite)
				}
			}
		}
		l.Annex = a
	}
	for _, art := range l.List {
		if o := originals[art.Number]; o != nil {
			art.Original = o
		}
	}
}

// otherReadings lists the readings of a decided row's fields that differ
// from what is shown. The ISF's typed copy of a row Law 278/2014 replaced
// prints the 2014 wording, which the row's own note already points to.
func otherReadings(raw []byte, shown map[string]string, replaced bool) []AnnexReading {
	var rd annexReadings
	if len(raw) == 0 || json.Unmarshal(raw, &rd) != nil {
		return nil
	}
	fields := make([]string, 0, len(rd))
	for f := range rd {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	var out []AnnexReading
	for _, f := range fields {
		seen := map[string]bool{strings.Join(strings.Fields(shown[f]), " "): true}
		for _, c := range rd[f].Candidates {
			isf := strings.HasPrefix(c.Source, "ISF")
			v := strings.Join(strings.Fields(c.Value), " ")
			if seen[v] || isf && replaced {
				continue
			}
			seen[v] = true
			src := "reader"
			if isf {
				src = "isf"
			}
			out = append(out, AnnexReading{Field: f, Value: c.Value, Source: src})
		}
	}
	return out
}

func uniqInts(xs []int) []int {
	out := xs[:0]
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}
