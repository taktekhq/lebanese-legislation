package site

import (
	"context"
	"strconv"
	"strings"

	"github.com/taktekhq/lebanese-legislation/internal/wordsearch"
)

// apiFindLawsEn adds the texts whose unofficial English title holds every
// word of a Latin-script query, after the Arabic matches (ranked below them):
// the laws part's last phase, where apiFindLaws left out.next. Arabic stays
// the official title; this only lets an English question find a text. They
// leave out the texts already on the page. An older database without the
// table adds nothing.
func (s *Server) apiFindLawsEn(ctx context.Context, q string, year, limit int, out *apiSearchResult) {
	at := out.next[partLaws]
	english := at.phase == lawsEnglish || at.phase == lawsEnglishOnly
	if english {
		out.next[partLaws] = searchPos{} // done unless rows are left below
	}
	seen := map[int]bool{}
	for _, id := range out.lookedUp {
		seen[id] = true
	}
	for _, l := range out.Laws {
		seen[l.ID] = true
	}
	room := limit - len(out.Laws)
	fetched := 1 // to count
	var from *wordsearch.Key
	if english {
		from = keyOf(&at)
		fetched = max(room, 0) + len(seen) + 1 // the page's laws may be among them
	}
	var except []wordsearch.Term
	if at.phase != lawsEnglishOnly { // the Arabic titles list these: count and list them once
		except = titleTerms(strings.Fields(q))
	}
	pg, laws, err := s.englishLaws(ctx, q, year, fetched, from, except)
	if err != nil || pg.Total == 0 {
		return
	}
	out.LawCount += pg.Full
	if !english {
		return
	}
	if room <= 0 {
		out.next[partLaws] = at
		return
	}
	last := -1
	for i, l := range laws {
		if len(out.Laws) >= limit {
			break
		}
		last = i
		if seen[l.ID] {
			continue
		}
		out.Laws = append(out.Laws, s.lawSum(ctx, l.LawRow, s.source(ctx, "law", l.ID)))
		seen[l.ID] = true
	}
	if last >= 0 && (last < len(laws)-1 || pg.More) {
		out.next[partLaws] = searchPos{phase: at.phase, key: keyField(laws[last].key)}
	}
}

// apiRescue is the zero-result rescue the search page runs (searchfallback.go)
// for /api/v1/search and the MCP search tool: when a search found nothing, the
// question is read again (English or French terms to Arabic, Latin names and
// Arabizi, a year or a number as a filter, a typo mended) and the first
// reading that finds something fills out, with searched_instead saying what
// was searched. typ is "" or one of laws, articles, rulings, gazette. It runs
// on the first page only, and its readings are not paged.
func (s *Server) apiRescue(ctx context.Context, q, typ string, year int, first bool, limit int, out *apiSearchResult) {
	if typ == "gazette" || year > 0 || !first || out.LawCount+out.ArtCount+out.RulCount+out.GazCount > 0 {
		return
	}
	alt, how, err := s.rescue(ctx, q)
	if err != nil || how == "" {
		return
	}
	out.SearchedInstead, out.SearchedInsteadKind = alt.Via, alt.ViaKind
	if typ == "" || typ == "laws" {
		out.LawCount = alt.LawCount
		for _, l := range alt.Laws[:min(limit, len(alt.Laws))] {
			out.Laws = append(out.Laws, s.lawSum(ctx, l, s.source(ctx, "law", l.ID)))
		}
	}
	if typ == "" || typ == "articles" {
		out.ArtCount = alt.ArtCount
		calculated := s.apiHitCalculator(ctx)
		for _, a := range alt.Articles[:min(limit, len(alt.Articles))] {
			out.Articles = append(out.Articles, apiArticleHit{Law: s.lawSum(ctx, a, s.source(ctx, "law", a.ID)),
				CalculatedCurrent: calculated(a.ID, a.Article), Number: a.Article, Snippet: stripMarks(a.Snippet), URL: Base + artPath(a.ID, a.Article)})
		}
	}
	if typ == "" || typ == "rulings" {
		out.RulCount = alt.RulCount
		for _, r := range alt.Rulings[:min(limit, len(alt.Rulings))] {
			h := apiRulingHit{ID: r.ID, Title: r.Title, Snippet: stripMarks(r.Snippet), URL: Base + "/ruling/" + strconv.Itoa(r.ID) + "/",
				UpdatedAt: s.listedChange(ctx, "ruling", r.ID)}
			if r.Date != nil {
				h.DecidedOn = r.Date.Format("2006-01-02")
			}
			out.Rulings = append(out.Rulings, h)
		}
	}
}
