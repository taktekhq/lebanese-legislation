package site

import (
	"context"
	"net/http"
	"strings"

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
	if pos.phase == phaseRanked {
		pg, hits, err := s.rankedLaws(ctx, first.lawQ, first.Year, first.Num, 0, morePerPage, keyOf(pos))
		if err != nil {
			return err
		}
		add(hits)
		switch {
		case pg.More && len(hits) > 0:
			res.LawCursor = moreCursor(partLaws, searchPos{phase: phaseRanked, key: keyField(hits[len(hits)-1].key)})
		case english:
			res.LawCursor = moreCursor(partLaws, searchPos{phase: lawsEnglish})
		}
	} else if english {
		var except []wordsearch.Term
		if pos.phase == lawsEnglish {
			except = titleTerms(strings.Fields(first.lawQ))
		}
		pg, hits, err := s.englishLaws(ctx, first.lawQ, 0, morePerPage, keyOf(pos), except)
		if err != nil {
			return err
		}
		add(hits)
		if pg.More && len(hits) > 0 {
			res.LawCursor = moreCursor(partLaws, searchPos{phase: pos.phase, key: keyField(hits[len(hits)-1].key)})
		}
	}
	if latin {
		for i := range res.Laws {
			res.Laws[i].TitleEn = s.titles.get(ctx, s, res.Laws[i].ID)
		}
	}
	return nil
}
