package site

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/taktekhq/lebanese-legislation/internal/cursor"
)

// Browsing by year: how Lebanese texts are cited ("Law 44/2015") and the way
// a static copy can list 100,000 texts without a server to filter them.

// YearCount is one year and how many documents it has; year 0 is undated.
type YearCount struct {
	Year  int
	Count int
}

// Decade groups years for the index page.
type Decade struct {
	Label string
	Years []YearCount
}

type yearKind struct {
	key, eyebrow, h1, lead, noun, many string
	yearsSQL                           string // year, count
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

// Years lists the years a kind of document has, for the static writer too.
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
		if k.key == "laws" { // the "laws" docKind doesn't exist; its noun/many live under "laws.count*"
			nounKey, manyKey = "laws.count", "laws.count_many"
		}
		h1 := s.trOr(r, stripNbsp(k.h1), "years.index."+k.key+".h1")
		eyebrow := s.trOr(r, k.eyebrow, "years.index."+k.key+".eyebrow")
		lead := s.trOr(r, k.lead, "years.index."+k.key+".lead")
		many := s.trOr(r, k.many, manyKey)
		noun := s.trOr(r, k.noun, nounKey)
		s.render(w, r, "years", 200, Page{Title: s.brandTitle(r, h1), Path: "/" + kind + "/years/", NoIndex: kind == "rulings" && rulingsHidden,
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
	pg, done := s.cursorPaging(w, r, true)
	if done {
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	// the year as a range on the sort key, which the list's index answers;
	// the undated sort as 0001-01-01 (keyset.go)
	key := dateKey("l.issued_on")
	cond, args := key+" = date '"+cursor.NoDate+"'", []any{}
	if y > 0 {
		cond, args = key+" >= make_date($1, 1, 1) AND "+key+" < make_date($1 + 1, 1, 1)", []any{y}
	}
	total, err := s.countRows(ctx, `SELECT count(*) FROM legislation l WHERE `+cond, args...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	base := fmt.Sprintf("/laws/year/%d/", y)
	pageArgs := append([]any{}, args...)
	page := pg.where(key, "l.id", &pageArgs)
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM (SELECT l.id FROM legislation l`+and(cond, page)+pg.order(key, "l.id")+
		fmt.Sprintf(" LIMIT %d", s.yearSize()+1)+`) p JOIN legislation l ON l.id = p.id JOIN legislation_kind k ON k.name_ar = l.kind`+pg.order(key, "l.id"), pageArgs...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := scanLawRows(rows)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, olderOK, newerOK, top := settle(pg, list, s.yearSize())
	if top {
		s.toFirstPage(w, r)
		return
	}
	older, newer := lawPageLinks(r.URL.Query(), list, olderOK, newerOK)
	label := yearLabel(y)
	if label == "no recorded date" {
		label = s.trOr(r, label, "years.no_recorded_date")
	}
	var subhubs []Chip
	if y > 0 && !s.isStatic {
		counts, err := s.yearKindCounts(ctx, y)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		subhubs = s.kindYearChips(r, y, counts)
	}
	s.render(w, r, "laws", 200, Page{
		Title: s.brandTitle(r, s.trOr(r, "Lebanese legislation of "+label, "years.laws_of_title", label)),
		Path:  base, NoIndex: !pg.first(),
		Desc: s.trOr(r, fmt.Sprintf("The %s Lebanese laws, decrees and decisions issued in %s. Free.", thousands(total), label),
			"years.laws_of_desc", thousands(total), label),
		Data: List{Total: total, Older: older, Newer: newer, Rows: list, Subhubs: subhubs, Back: "/laws/years/",
			Updated: s.pageUpdated(r, s.latestChange(ctx, cond, args...), nil), BackLabel: s.trOr(r, "All years", "years.all_years"),
			Eyebrow: s.trOr(r, "Legislation · "+label, "years.legislation_of", label), Heading: s.trOr(r, "Texts of "+label+".", "years.texts_of", label)}})
}

// yearSize is how many texts a year's list shows on a page; a static copy has
// no pager to follow, so it shows a year whole.
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
		pg, done := s.cursorPaging(w, r, true)
		if done {
			return
		}
		ctx, cancel := s.ctx(r)
		defer cancel()
		// each list's rows by its index (db/indexes.sql, "Lists"), then the
		// page by its cursor (keyset.go)
		var table, cond, key, sql string
		switch kind {
		case "rulings":
			table, key, cond = "ruling", dateKey("decided_on"), "coalesce(year, 0) = $1"
			sql = `SELECT 'ruling', r.id, r.title, coalesce(r.court, ''), r.decided_on, left(r.text_ar, 220) FROM ruling r JOIN (%s) p ON p.id = r.id`
		case "gazette":
			table, key = "gazette_issue", dateKey("published_on")
			cond = key + " >= make_date($1, 1, 1) AND " + key + " < make_date($1 + 1, 1, 1)"
			if y == 0 {
				cond = "published_on IS NULL"
			}
			sql = `SELECT 'gazette', g.id, 'الجريدة الرسمية، العدد ' || coalesce(g.number, '؟'),
				(SELECT count(*) FROM gazette_entry e WHERE e.issue_id = g.id)::text || ' texts', g.published_on, NULL
				FROM gazette_issue g JOIN (%s) p ON p.id = g.id`
		}
		args := []any{y}
		if y == 0 && kind == "gazette" {
			args = nil
		}
		total, err := s.countRows(ctx, "SELECT count(*) FROM "+table+and(cond), args...)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		base := fmt.Sprintf("/%s/year/%d/", kind, y)
		page := pg.where(key, "id", &args)
		ids := "SELECT id, " + key + " AS key FROM " + table + and(cond, page) + pg.order(key, "id") + fmt.Sprintf(" LIMIT %d", s.yearSize()+1)
		rows, err := s.db.Query(ctx, fmt.Sprintf(sql, ids)+pg.order("p.key", "p.id"), args...)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		list, err := scanDocRows(rows)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		list, olderOK, newerOK, top := settle(pg, list, s.yearSize())
		if top {
			s.toFirstPage(w, r)
			return
		}
		older, newer := docPageLinks(r.URL.Query(), list, true, olderOK, newerOK)
		label := yearLabel(y)
		if label == "no recorded date" {
			label = s.trOr(r, label, "years.no_recorded_date")
		}
		eyebrow := s.trOr(r, k.eyebrow, "years.index."+k.key+".eyebrow")
		many := s.trOr(r, k.many, k.key+".many")
		noun := s.trOr(r, k.noun, k.key+".noun")
		s.render(w, r, "list", 200, Page{Title: s.brandTitle(r, stripNbsp(eyebrow)+" "+label),
			Path: base, NoIndex: (kind == "rulings" && rulingsHidden) || !pg.first(),
			Desc: s.trOr(r, fmt.Sprintf("The %s %s of %s. Free.", thousands(total), k.many, label),
				"years.docs_of_desc", thousands(total), many, label),
			Data: map[string]any{"Kind": map[string]string{"Eyebrow": eyebrow + " · " + label, "H1": label + ".",
				"Noun": noun, "Many": many, "Back": "/" + kind + "/years/", "BackLabel": s.trOr(r, "All years", "years.all_years")},
				"List": List{Total: total, Older: older, Newer: newer, Rows: list}}})
	}
}

func yearLabel(y int) string {
	if y == 0 {
		return "no recorded date"
	}
	return strconv.Itoa(y)
}

func stripNbsp(s string) string { return strings.ReplaceAll(s, "&nbsp;", " ") }
