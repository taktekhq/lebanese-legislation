package site

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type YearCount struct {
	Year  int
	Count int
}

type Decade struct {
	Label string
	Years []YearCount
}

type yearKind struct {
	key, eyebrow, h1, lead, noun, many string
	yearsSQL                           string
}

var yearKinds = map[string]yearKind{
	"laws": {key: "laws", eyebrow: "Legislation · التشريعات", h1: "Legislation, year by&nbsp;year.",
		lead: "Every law, decree and decision, by the year it was issued.", noun: "text", many: "texts",
		yearsSQL: `SELECT coalesce(extract(year FROM issued_on)::int, 0), count(*)::int FROM legislation GROUP BY 1 ORDER BY 1 DESC`},
	"rulings": {key: "rulings", eyebrow: "Court rulings · الأحكام", h1: "Rulings, year by&nbsp;year.",
		lead: "Every ruling, by the year it was decided.", noun: "ruling", many: "rulings",
		yearsSQL: `SELECT coalesce(year, 0), count(*)::int FROM ruling GROUP BY 1 ORDER BY 1 DESC`},
	"gazette": {key: "gazette", eyebrow: "Official Gazette · الجريدة الرسمية", h1: "The Official Gazette, year by&nbsp;year.",
		lead: "Every issue, by the year it was published.", noun: "issue", many: "issues",
		yearsSQL: `SELECT coalesce(extract(year FROM published_on)::int, 0), count(*)::int FROM gazette_issue GROUP BY 1 ORDER BY 1 DESC`},
}

func (s *Server) Years(ctx context.Context, kind string) ([]YearCount, error) {
	rows, err := s.db.Query(ctx, yearKinds[kind].yearsSQL)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[YearCount])
}

func (s *Server) yearIndex(kind string) http.HandlerFunc {
	k := yearKinds[kind]
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := s.ctx(r)
		defer cancel()
		ys, err := s.Years(ctx, kind)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var decades []Decade
		total := 0
		undated := s.trOr(r, "Undated", "years.undated")
		for _, y := range ys {
			total += y.Count
			label := undated
			if y.Year > 0 {
				label = fmt.Sprintf("%ds", y.Year/10*10)
			}
			if len(decades) == 0 || decades[len(decades)-1].Label != label {
				decades = append(decades, Decade{Label: label})
			}
			d := &decades[len(decades)-1]
			d.Years = append(d.Years, y)
		}
		nounKey, manyKey := k.key+".noun", k.key+".many"
		if k.key == "laws" {
			nounKey, manyKey = "laws.count", "laws.count_many"
		}
		h1 := s.trOr(r, stripNbsp(k.h1), "years.index."+k.key+".h1")
		eyebrow := s.trOr(r, k.eyebrow, "years.index."+k.key+".eyebrow")
		lead := s.trOr(r, k.lead, "years.index."+k.key+".lead")
		many := s.trOr(r, k.many, manyKey)
		noun := s.trOr(r, k.noun, nounKey)
		s.render(w, r, "years", 200, Page{Title: s.brandTitle(r, h1), Path: "/" + kind + "/years/",
			Desc: s.trOr(r, fmt.Sprintf("%s %s, by year. Free.", thousands(total), k.many), "years.index.desc", thousands(total), many),
			Data: map[string]any{"Kind": k.key, "Eyebrow": eyebrow, "H1": h1, "Lead": lead, "Total": total,
				"Noun": noun, "Many": many, "Decades": decades}})
	}
}

func yearParam(r *http.Request) (int, bool) {
	y, err := strconv.Atoi(r.PathValue("year"))
	return y, err == nil && (y == 0 || (y > 1800 && y < 2100))
}

func (s *Server) lawsYear(w http.ResponseWriter, r *http.Request) {
	y, ok := yearParam(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	cond, args := "l.issued_on IS NULL", []any{}
	if y > 0 {
		cond, args = "l.issued_on >= make_date($1, 1, 1) AND l.issued_on < make_date($1 + 1, 1, 1)", []any{y}
	}
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM legislation l WHERE `+cond, args...).Scan(&total); err != nil {
		s.fail(w, r, err)
		return
	}
	base := fmt.Sprintf("/laws/year/%d/", y)
	page, pages, listBase, done := s.listPaging(w, r, base, r.URL.Query(), total, s.yearSize())
	if done {
		return
	}
	rows, err := s.db.Query(ctx, fmt.Sprintf(`SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE `+cond+` ORDER BY l.issued_on DESC NULLS LAST, l.id DESC LIMIT %d OFFSET %d`, s.yearSize(), (page-1)*s.yearSize()), args...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := scanLawRows(rows)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	label := yearLabel(y)
	if label == "no recorded date" {
		label = s.trOr(r, label, "years.no_recorded_date")
	}
	s.render(w, r, "laws", 200, Page{
		Title: s.brandTitle(r, s.trOr(r, "Lebanese legislation of "+label, "years.laws_of_title", label)) + s.pageSuffix(r, page),
		Path:  pagePath(base, page),
		Desc: s.trOr(r, fmt.Sprintf("The %s Lebanese laws, decrees and decisions issued in %s. Free.", thousands(total), label),
			"years.laws_of_desc", thousands(total), label),
		Data: List{Total: total, Page: page, Pages: pages, Rows: list, Base: listBase, Back: "/laws/years/", BackLabel: s.trOr(r, "All years", "years.all_years"),
			Eyebrow: s.trOr(r, "Legislation · "+label, "years.legislation_of", label), Heading: s.trOr(r, "Texts of "+label+".", "years.texts_of", label)}})
}

func (s *Server) yearSize() int {
	if s.isStatic {
		return 1 << 30
	}
	return yearPageSize
}

func (s *Server) docYear(kind string) http.HandlerFunc {
	k := yearKinds[kind]
	return func(w http.ResponseWriter, r *http.Request) {
		y, ok := yearParam(r)
		if !ok {
			s.notFound(w, r)
			return
		}
		ctx, cancel := s.ctx(r)
		defer cancel()
		var sql, countSQL string
		switch kind {
		case "rulings":
			countSQL = `SELECT count(*) FROM ruling WHERE coalesce(year, 0) = $1`
			sql = `SELECT 'ruling', id, title, coalesce(court, ''), decided_on, left(text_ar, 220) FROM ruling
				WHERE coalesce(year, 0) = $1 ORDER BY decided_on DESC NULLS LAST, id DESC`
		case "gazette":
			countSQL = `SELECT count(*) FROM gazette_issue WHERE coalesce(extract(year FROM published_on)::int, 0) = $1`
			sql = `SELECT 'gazette', g.id, 'الجريدة الرسمية، العدد ' || coalesce(g.number, '؟'),
				(SELECT count(*) FROM gazette_entry e WHERE e.issue_id = g.id)::text || ' texts', g.published_on, NULL
				FROM gazette_issue g WHERE coalesce(extract(year FROM g.published_on)::int, 0) = $1
				ORDER BY g.published_on DESC NULLS LAST, g.id DESC`
		}
		var total int
		if err := s.db.QueryRow(ctx, countSQL, y).Scan(&total); err != nil {
			s.fail(w, r, err)
			return
		}
		base := fmt.Sprintf("/%s/year/%d/", kind, y)
		page, pages, listBase, done := s.listPaging(w, r, base, r.URL.Query(), total, s.yearSize())
		if done {
			return
		}
		rows, err := s.db.Query(ctx, fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, s.yearSize(), (page-1)*s.yearSize()), y)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		list, err := scanDocRows(rows)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		label := yearLabel(y)
		if label == "no recorded date" {
			label = s.trOr(r, label, "years.no_recorded_date")
		}
		eyebrow := s.trOr(r, k.eyebrow, "years.index."+k.key+".eyebrow")
		many := s.trOr(r, k.many, k.key+".many")
		noun := s.trOr(r, k.noun, k.key+".noun")
		s.render(w, r, "list", 200, Page{Title: s.brandTitle(r, stripNbsp(eyebrow)+" "+label) + s.pageSuffix(r, page),
			Path: pagePath(base, page),
			Desc: s.trOr(r, fmt.Sprintf("The %s %s of %s. Free.", thousands(total), k.many, label),
				"years.docs_of_desc", thousands(total), many, label),
			Data: map[string]any{"Kind": map[string]string{"Eyebrow": eyebrow + " · " + label, "H1": label + ".",
				"Noun": noun, "Many": many, "Back": "/" + kind + "/years/", "BackLabel": s.trOr(r, "All years", "years.all_years")},
				"List": List{Total: total, Page: page, Pages: pages, Rows: list, Base: listBase}}})
	}
}

func yearLabel(y int) string {
	if y == 0 {
		return "no recorded date"
	}
	return strconv.Itoa(y)
}

func stripNbsp(s string) string { return strings.ReplaceAll(s, "&nbsp;", " ") }
