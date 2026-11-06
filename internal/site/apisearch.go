package site

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// apiFindLawsEn adds the texts whose unofficial English title holds every
// word of a Latin-script query, after the Arabic matches (ranked below them):
// the laws part's last phase, where apiFindLaws left out.next. Arabic stays
// the official title; this only lets an English question find a text. They
// rank codes first, then the newest, then the highest id, and leave out the
// texts the Arabic titles found, so no text is listed twice. An older
// database without the table adds nothing.
func (s *Server) apiFindLawsEn(ctx context.Context, q string, year, limit int, out *apiSearchResult) {
	at := out.next[partLaws]
	english := at.phase == lawsEnglish || at.phase == lawsEnglishOnly
	if english {
		out.next[partLaws] = searchPos{} // done unless rows are left below
	}
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
	out.LawCount += n
	if !english {
		return
	}
	out.next[partLaws] = at
	room := limit - len(out.Laws)
	if room <= 0 {
		return
	}
	if at.phase == lawsEnglish { // the Arabic titles listed these already
		if title, targs := lawTitleWhere(q, year, len(args)+1); title != "" {
			cond += " AND (" + title + ") IS NOT TRUE"
			args = append(args, targs...)
		}
	}
	keyset := ""
	if at.key != nil {
		args = append(args, argInt(at.key[0]), argDate(at.key[2]), argInt(at.key[3]))
		keyset = fmt.Sprintf(" WHERE (t.is_code, t.d, t.id) < ($%d::int, $%d::date, $%d::int)", len(args)-2, len(args)-1, len(args))
	}
	seen := map[int]bool{}
	for _, id := range out.lookedUp {
		seen[id] = true
	}
	for _, l := range out.Laws {
		seen[l.ID] = true
	}
	fetched := room + len(seen) + 1 // the page's laws may be among them
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+`, p.is_code, p.d FROM (
		SELECT t.* FROM (SELECT l.id, (EXISTS (SELECT 1 FROM code c WHERE c.law_id = l.id))::int AS is_code,
			coalesce(l.issued_on, date '0001-01-01') AS d
			FROM legislation_title_en te JOIN legislation l ON l.id = te.law_id WHERE `+cond+`) t`+keyset+`
		ORDER BY t.is_code DESC, t.d DESC, t.id DESC LIMIT `+strconv.Itoa(fetched)+`) p
		JOIN legislation l ON l.id = p.id JOIN legislation_kind k ON k.name_ar = l.kind
		ORDER BY p.is_code DESC, p.d DESC, p.id DESC`, args...)
	if err != nil {
		return
	}
	laws, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (rankedLaw, error) {
		var l rankedLaw
		err := row.Scan(append(lawRowDest(&l.LawRow), &l.code, &l.on)...)
		return l, err
	})
	if err != nil {
		return
	}
	last, more := fill(len(out.Laws), limit, laws, fetched, func(l rankedLaw) bool { return seen[l.ID] }, func(l rankedLaw) {
		out.Laws = append(out.Laws, s.lawSum(ctx, l.LawRow, s.source(ctx, "law", l.ID)))
		seen[l.ID] = true
	})
	out.next[partLaws] = searchPos{}
	if more {
		out.next[partLaws] = after(&at, at.phase, last, func(i int) []string { return laws[i].key() })
	}
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
