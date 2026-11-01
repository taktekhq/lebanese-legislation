package site

import (
	"context"
	"strconv"
	"strings"
)

// apiFindLawsEn adds the texts whose unofficial English title holds every
// word of a Latin-script query, after the Arabic matches already in out
// (ranked below them). Arabic stays the official title; this only lets an
// English question find a text. An older database without the table adds
// nothing.
func (s *Server) apiFindLawsEn(ctx context.Context, q string, year, limit, offset int, out *apiSearchResult) {
	pats := englishTitlePats(q)
	if len(pats) == 0 {
		return
	}
	cond := `te.title_en ILIKE ALL($1)`
	args := []any{pats}
	if year > 0 {
		args = append(args, year)
		cond += ` AND extract(year from l.issued_on)::int = $2`
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM legislation_title_en te JOIN legislation l ON l.id = te.law_id WHERE `+cond, args...).Scan(&n); err != nil || n == 0 {
		return
	}
	seen := map[int]bool{}
	for _, l := range out.Laws {
		seen[l.ID] = true
	}
	// Offset applies to the Arabic matches first; English ones fill what is left of the page.
	room := limit - len(out.Laws)
	if room <= 0 {
		out.LawCount += n
		return
	}
	args = append(args, room+len(seen), max(0, offset-out.LawCount))
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation_title_en te JOIN legislation l ON l.id = te.law_id
		JOIN legislation_kind k ON k.name_ar = l.kind WHERE `+cond+` ORDER BY (EXISTS (SELECT 1 FROM code c WHERE c.law_id = l.id)) DESC,
		l.issued_on DESC NULLS LAST LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return
	}
	laws, err := scanLawRows(rows)
	if err != nil {
		return
	}
	added := 0
	for _, l := range laws {
		if seen[l.ID] || room <= 0 {
			continue
		}
		out.Laws = append(out.Laws, s.lawSum(ctx, l, s.source(ctx, "law", l.ID)))
		seen[l.ID], room = true, room-1
		added++
	}
	out.LawCount += n
}

// englishTitlePats are ILIKE patterns, one per word of a Latin-script query
// (none for Arabic, or when only little words are left).
func englishTitlePats(q string) []string {
	if hasArabic(q) {
		return nil
	}
	var pats []string
	for _, t := range latinTokens(q) {
		if glossStop[t] || len(t) < 2 {
			continue
		}
		pats = append(pats, "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(t)+"%")
	}
	return pats
}

// lawsByEnglishTitle is the search page's use of the English titles: the
// texts whose title_en holds every word of a Latin-script query, codes first,
// then the shortest title (the closest to the words), newest on ties. An older
// database without the table finds nothing.
func (s *Server) lawsByEnglishTitle(ctx context.Context, q string, limit int) ([]LawRow, int) {
	pats := englishTitlePats(q)
	if len(pats) == 0 {
		return nil, 0
	}
	const cond = `te.title_en ILIKE ALL($1)`
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM legislation_title_en te WHERE `+cond, pats).Scan(&n); err != nil || n == 0 {
		return nil, 0
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation_title_en te JOIN legislation l ON l.id = te.law_id
		JOIN legislation_kind k ON k.name_ar = l.kind WHERE `+cond+` ORDER BY (EXISTS (SELECT 1 FROM code c WHERE c.law_id = l.id)) DESC,
		(l.status <> 'repealed') DESC, length(te.title_en), l.issued_on DESC NULLS LAST LIMIT $2`, pats, limit)
	if err != nil {
		return nil, 0
	}
	laws, err := scanLawRows(rows)
	if err != nil {
		return nil, 0
	}
	return laws, n
}

// apiRescue is the zero-result rescue the search page runs (searchfallback.go)
// for /api/v1/search and the MCP search tool: when a search found nothing, the
// question is read again (English or French terms to Arabic, Latin names and
// Arabizi, a year or a number as a filter, a typo mended) and the first
// reading that finds something fills out, with searched_instead saying what
// was searched. typ is "" or one of laws, articles, rulings, gazette.
func (s *Server) apiRescue(ctx context.Context, q, typ string, year, offset int, limit int, out *apiSearchResult) {
	if typ == "gazette" || year > 0 || offset > 0 || out.LawCount+out.ArtCount+out.RulCount+out.GazCount > 0 {
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
			h := apiRulingHit{ID: r.ID, Title: r.Title, Snippet: stripMarks(r.Snippet), URL: Base + "/ruling/" + strconv.Itoa(r.ID) + "/"}
			if r.Date != nil {
				h.DecidedOn = r.Date.Format("2006-01-02")
			}
			out.Rulings = append(out.Rulings, h)
		}
	}
}
