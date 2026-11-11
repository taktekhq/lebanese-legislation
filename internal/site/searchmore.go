package site

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/taktekhq/lebanese-legislation/internal/cursor"
	"github.com/taktekhq/lebanese-legislation/internal/wordsearch"
)

// "Show more" on the search page. A search lists the first ten texts,
// fifteen articles and ten rulings beside the count of each; before 11 Nov
// 2026 the rest could not be reached (a visitor asked the search box how to
// scroll through the 63 it counted). Each part that has rows left now ends
// with a link to its next page:
//
//	/search/?q=<the search>&part=laws|articles|rulings&cursor=<cursor>
//
// The cursor is the one /api/v1/search/ pages with (searchcursor.go): the
// rank of the last row shown, never an offset. The address answers a whole
// page listing only those rows, so it works without scripts; with &frag=1 it
// answers the rows alone, which legislation.js appends under the list.

// morePerPage is how many rows one "show more" brings.
const morePerPage = 50

// moreParts are the parts of a search the page can continue, by name.
var moreParts = map[string]int{"laws": partLaws, "articles": partArticles, "rulings": partRulings}

// moreCursor is the cursor of one part standing at pos.
func moreCursor(part int, pos searchPos) string {
	var next [4]searchPos
	next[part] = pos
	return encodeSearchCursor(searchParts[part].name, next)
}

func (s *Server) searchMore(w http.ResponseWriter, r *http.Request, q, name string) {
	if name == "cited" || name == "cited-rulings" {
		s.searchMoreCited(w, r, q, name)
		return
	}
	part, ok := moreParts[name]
	if !ok {
		http.Error(w, "part is laws, articles or rulings", http.StatusBadRequest)
		return
	}
	cur, ok := decodeSearchCursor(r.URL.Query().Get("cursor"), name)
	if !ok || cur.pos(part).phase == phaseDone {
		http.Error(w, "cursor is not one this page made", http.StatusBadRequest)
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	// The search itself, remembered for an hour: its counts, the words it
	// read and the rows the first page showed.
	first, _, err := s.searchCached(ctx, q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	res := Results{Q: first.Q, Part: name, LawCount: first.LawCount, ArtCount: first.ArtCount, RulCount: first.RulCount}
	switch part {
	case partLaws:
		err = s.moreLaws(ctx, first, cur.pos(part), &res)
	case partArticles:
		err = s.moreArticles(ctx, first, cur.pos(part), &res)
	case partRulings:
		err = s.moreRulings(ctx, first, cur.pos(part), &res)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.renderMore(w, r, res)
}

// renderMore answers a page of next rows: the rows alone with frag=1, else a
// whole page.
func (s *Server) renderMore(w http.ResponseWriter, r *http.Request, res Results) {
	if r.URL.Query().Get("frag") == "1" {
		pages := s.pages
		switch lang := s.lang(r); {
		case lang == "ar" && s.pagesAr != nil:
			pages = s.pagesAr
		case lang == "fr" && s.pagesFr != nil:
			pages = s.pagesFr
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Robots-Tag", "noindex")
		if err := pages["search"].ExecuteTemplate(w, "results", res); err != nil {
			s.captureError(r, "search more render", err)
		}
		return
	}
	title := res.Q + " · " + s.brandTitle(r, s.trOr(r, "Search", "search.title"))
	s.render(w, r, "search", 200, Page{Title: title, Path: "/search/", NoIndex: true,
		Desc: s.trOr(r, "Search every Lebanese law, article and court ruling, in Arabic or French.", "search.desc"), Data: res})
}

// moreEnglish ends the texts with a link into the English titles of q when
// those hold more texts than the page lists and the Arabic titles gave no
// link of their own (theirs leads on to the English ones, moreLaws).
func (res *Results) moreEnglish(q string) {
	if res.LawCursor != "" || res.LawCount <= len(res.Laws) || hasArabic(q) || len(latinTokens(q)) == 0 {
		return
	}
	phase := lawsEnglishOnly
	if res.lawFull > 0 && len(titleTerms(strings.Fields(q))) > 0 {
		phase = lawsEnglish // leave out what the Arabic titles found
	}
	res.lawQ, res.LawCursor = q, moreCursor(partLaws, searchPos{phase: phase})
}

// moreArticles is the next page of the articles, by rank.
func (s *Server) moreArticles(ctx context.Context, first Results, pos *searchPos, res *Results) error {
	if first.artQ == "" {
		return nil
	}
	pg, hits, last, err := s.rankedArticles(ctx, first.artQ, morePerPage, keyOf(pos), false)
	if err != nil {
		return err
	}
	for _, h := range hits {
		res.Articles = append(res.Articles, h.LawRow)
	}
	if pg.More && len(hits) > 0 {
		res.ArtCursor = moreCursor(partArticles, searchPos{phase: phaseRanked, key: keyField(last)})
	}
	return nil
}

// moreRulings is the next page of the rulings: titles and summaries, then
// the judgments (rulingsPage).
func (s *Server) moreRulings(ctx context.Context, first Results, pos *searchPos, res *Results) error {
	if first.rulQ == "" {
		return nil
	}
	hits, _, next, err := s.rulingsPage(ctx, first.rulQ, morePerPage, pos)
	if err != nil {
		return err
	}
	for _, h := range hits {
		res.Rulings = append(res.Rulings, DocRow{Type: "ruling", ID: h.ID, Title: h.Title, Kicker: h.Court, Date: h.On, Snippet: h.Snippet})
	}
	if next.phase != phaseDone {
		res.RulCursor = moreCursor(partRulings, next)
	}
	return nil
}

// moreLaws is the next page of the texts: the Arabic titles by rank from the
// first one, then the unofficial English titles the Arabic ones did not
// find, each without the texts the first page showed (it reorders its ten).
func (s *Server) moreLaws(ctx context.Context, first Results, pos *searchPos, res *Results) error {
	if first.lawQ == "" {
		return nil
	}
	shown := make(map[int]bool, len(first.Laws))
	for _, l := range first.Laws {
		shown[l.ID] = true
	}
	add := func(hits []lawHit) {
		for _, h := range hits {
			if !shown[h.ID] {
				res.Laws = append(res.Laws, h.LawRow)
			}
		}
	}
	latin := !hasArabic(first.lawQ) && len(latinTokens(first.lawQ)) > 0
	english := first.Num == "" && first.Year == 0 && latin
	phase, key := pos.phase, keyOf(pos)
	if phase == phaseRanked {
		pg, hits, err := s.rankedLaws(ctx, first.lawQ, first.Year, first.Num, 0, morePerPage, key)
		if err != nil {
			return err
		}
		add(hits)
		if pg.More && len(hits) > 0 {
			res.LawCursor = moreCursor(partLaws, searchPos{phase: phaseRanked, key: keyField(hits[len(hits)-1].key)})
			english = false
		}
		// The Arabic titles end here: the English ones follow on this page,
		// so no link leads to an empty one.
		phase, key = lawsEnglish, nil
	}
	if english {
		var except []wordsearch.Term
		if phase == lawsEnglish {
			except = titleTerms(strings.Fields(first.lawQ))
		}
		pg, hits, err := s.englishLaws(ctx, first.lawQ, 0, morePerPage, key, except)
		if err != nil {
			return err
		}
		add(hits)
		if pg.More && len(hits) > 0 {
			res.LawCursor = moreCursor(partLaws, searchPos{phase: phase, key: keyField(hits[len(hits)-1].key)})
		}
	}
	if latin {
		for i := range res.Laws {
			res.Laws[i].TitleEn = s.titles.get(ctx, s, res.Laws[i].ID)
		}
	}
	return nil
}

// A citation lookup ("مرسوم 100", "1/2012", a court and a year) is not ranked
// by words: it lists the ten texts or rulings nearest to what was typed, in
// an order of its own. Its next pages list every match newest first, by the
// date and id of the last row shown (never an offset), without the ten the
// first page showed.

// citeMore is what a citation lookup matched: a condition on legislation l,
// or on ruling, and its arguments.
type citeMore struct {
	cond string
	args []any
}

// citeStart is the cursor before the newest row.
var citeStart = cursor.DateID("9999-12-31", 1<<31-1)

// moreCited ends the texts (or the rulings) of a citation lookup with a link
// when it matched more than the page lists.
func (res *Results) moreCited(rulings bool, cond string, args []any) {
	c := &citeMore{cond: cond, args: append([]any{}, args...)}
	switch {
	case rulings && res.RulCount > len(res.Rulings):
		res.rulCite, res.RulCursor = c, citeStart
	case !rulings && res.LawCount > len(res.Laws):
		res.lawCite, res.LawCursor = c, citeStart
	}
}

// LawPart and RulPart name the part a "show more" link continues.
func (res Results) LawPart() string {
	if res.lawCite != nil {
		return "cited"
	}
	return "laws"
}

func (res Results) RulPart() string {
	if res.rulCite != nil {
		return "cited-rulings"
	}
	return "rulings"
}

func (s *Server) searchMoreCited(w http.ResponseWriter, r *http.Request, q, name string) {
	date, id, ok := cursor.ParseDateID(r.URL.Query().Get("cursor"))
	if !ok || id < 0 || id > 1<<31-1 {
		http.Error(w, "cursor is not one this page made", http.StatusBadRequest)
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	first, _, err := s.searchCached(ctx, q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	res := Results{Q: first.Q, Part: name, LawCount: first.LawCount, ArtCount: first.ArtCount, RulCount: first.RulCount}
	shown := map[int]bool{}
	if name == "cited" && first.lawCite != nil {
		c := first.lawCite
		res.lawCite = c
		for _, l := range first.Laws {
			shown[l.ID] = true
		}
		n := len(c.args)
		// The condition is the lookup's own text, never typed words; it is
		// joined, not formatted, so nothing in it reads as a verb.
		rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
			WHERE (`+c.cond+`) AND (coalesce(l.issued_on, '0001-01-01'::date), l.id) < `+fmt.Sprintf("($%d::date, $%d)", n+1, n+2)+`
			ORDER BY coalesce(l.issued_on, '0001-01-01'::date) DESC, l.id DESC LIMIT `+fmt.Sprintf("$%d", n+3),
			append(append([]any{}, c.args...), date, id, morePerPage+len(shown)+1)...)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		laws, err := scanLawRows(rows)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		more := len(laws) > morePerPage+len(shown)
		if more {
			laws = laws[:len(laws)-1]
		}
		for _, l := range laws {
			if !shown[l.ID] {
				res.Laws = append(res.Laws, l)
			}
		}
		if more {
			last := laws[len(laws)-1]
			res.LawCursor = cursor.DateID(cursor.Date(last.IssuedOn), int64(last.ID))
		}
	}
	if name == "cited-rulings" && first.rulCite != nil {
		c := first.rulCite
		res.rulCite = c
		for _, d := range first.Rulings {
			shown[d.ID] = true
		}
		n := len(c.args)
		rows, err := s.db.Query(ctx, `SELECT id, title, coalesce(court, ''), decided_on FROM ruling
			WHERE (`+c.cond+`) AND (coalesce(decided_on, '0001-01-01'::date), id) < `+fmt.Sprintf("($%d::date, $%d)", n+1, n+2)+`
			ORDER BY coalesce(decided_on, '0001-01-01'::date) DESC, id DESC LIMIT `+fmt.Sprintf("$%d", n+3),
			append(append([]any{}, c.args...), date, id, morePerPage+len(shown)+1)...)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		found, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (DocRow, error) {
			d := DocRow{Type: "ruling"}
			err := row.Scan(&d.ID, &d.Title, &d.Kicker, &d.Date)
			return d, err
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		more := len(found) > morePerPage+len(shown)
		if more {
			found = found[:len(found)-1]
		}
		for _, d := range found {
			if !shown[d.ID] {
				res.Rulings = append(res.Rulings, d)
			}
		}
		if more {
			last := found[len(found)-1]
			res.RulCursor = cursor.DateID(cursor.Date(last.Date), int64(last.ID))
		}
	}
	s.renderMore(w, r, res)
}
