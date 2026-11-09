package mcpserver

import (
	"context"
	"sort"
	"strings"

	"github.com/taktekhq/lebanese-legislation/internal/wordsearch"
)

// The MCP server searches with internal/wordsearch, as the website and the
// API do: one ranked query per kind of text, from the indexes and the word
// counts the build makes. These read its hits into the tools' rows.

// topRanked: the articles read in full to put an exact phrase first among
// the best ones.
const topRanked = 50

// The searches read only their hits (Hit.Full, never Page.Total), so they
// ask for no counts (NoCount).

// knownVersion is the mark db/prod.sql puts on search_known.
const knownVersion = "lebleg-prod 3"

// known is what the searches read once instead of joining it to every
// candidate (wordsearch.Known): the codes and the laws of a kind the site
// does not serve (none today), from search_known (db/prod.sql). The
// database does not change while the server runs. Without the table (or
// with another mark) the searches join as before (nil); a failed read is
// not remembered.
func (s *Server) known(ctx context.Context) *wordsearch.Known {
	if k := s.searchKnown.Load(); k != nil {
		if k.Codes == nil {
			return nil // no search_known in this database
		}
		return k
	}
	var mark *string
	if err := s.db.QueryRow(ctx, `SELECT obj_description(to_regclass('search_known'), 'pg_class')`).Scan(&mark); err != nil {
		return nil
	}
	k := &wordsearch.Known{}
	if mark != nil && *mark == knownVersion {
		if err := s.db.QueryRow(ctx, `SELECT codes, unserved FROM search_known`).Scan(&k.Codes, &k.Unserved); err != nil {
			return nil
		}
		if k.Codes == nil {
			k.Codes = []int32{}
		}
	}
	s.searchKnown.CompareAndSwap(nil, k)
	return s.known(ctx)
}

// rankedLaws are the laws whose titles (Arabic, or the unofficial English
// with en) hold the question's words, best first. full is how many of them
// hold every word.
func (s *Server) rankedLaws(ctx context.Context, q string, en bool, year yearVal, num string, limit int) (laws []lawRow, full int, err error) {
	y := 0
	if year.positive() {
		y = int(truncf(year.f))
	}
	pg, err := wordsearch.Laws(ctx, s.db, wordsearch.LawQuery{Text: q, English: en, Year: y, Number: num, Limit: limit,
		Known: s.known(ctx), NoCount: true})
	if err != nil || len(pg.Hits) == 0 {
		return nil, 0, err
	}
	ids := make([]int32, len(pg.Hits))
	for i, h := range pg.Hits {
		ids[i] = int32(h.ID)
		if h.Full {
			full++
		}
	}
	rows, err := s.db.Query(ctx, `SELECT `+s.sql(ctx).lawCols+` FROM legislation l `+s.sql(ctx).lawJoins+` WHERE l.id = ANY($1) ORDER BY array_position($1, l.id)`, ids)
	if err != nil {
		return nil, 0, err
	}
	laws, err = scanLaws(rows, nil)
	return laws, full, err
}

// rankedArticles are the articles that hold the question's words, best
// first, with an exact phrase first among the best ones that hold as many
// words. lawID keeps them to one text; fr searches the French texts.
func (s *Server) rankedArticles(ctx context.Context, q string, fr bool, lawID, limit int) (hits []articleHit, full int, err error) {
	pg, err := wordsearch.Articles(ctx, s.db, wordsearch.ArticleQuery{Text: q, French: fr, LawID: lawID, Titled: true, Limit: max(limit, topRanked),
		Known: s.known(ctx), NoCount: true})
	if err != nil || len(pg.Hits) == 0 {
		return nil, 0, err
	}
	top := pg.Hits
	sort.SliceStable(top, func(i, j int) bool {
		a, b := top[i], top[j]
		switch {
		case a.Key.Units != b.Key.Units:
			return a.Key.Units > b.Key.Units
		case a.Code != b.Code:
			return a.Code
		case a.Key.Titled != b.Key.Titled:
			return a.Key.Titled
		}
		return a.Phrase && !b.Phrase
	})
	top = top[:min(limit, len(top))]
	cs := make([]candidate, len(top))
	for i, h := range top {
		cs[i] = candidate{h.ID, h.Position}
		if h.Full {
			full++
		}
	}
	col := "a.text_ar"
	if fr {
		col = "a.text_fr"
	}
	hits, err = s.hitsFor(ctx, cs, col)
	return hits, full, err
}

// rankedRulings are the rulings whose title or text holds the question's
// words, best first.
func (s *Server) rankedRulings(ctx context.Context, q string, limit int) (out []any, full int, err error) {
	pg, err := wordsearch.Rulings(ctx, s.db, wordsearch.RulingQuery{Text: q, Limit: limit, Known: s.known(ctx), NoCount: true})
	out = []any{}
	if err != nil || len(pg.Hits) == 0 {
		return out, 0, err
	}
	ids := make([]int32, len(pg.Hits))
	for i, h := range pg.Hits {
		ids[i] = int32(h.ID)
		if h.Full {
			full++
		}
	}
	rows, err := s.db.Query(ctx, `SELECT r.id, r.title, r.court, to_char(r.decided_on, 'YYYY-MM-DD'), r.text_ar,
		EXISTS (SELECT 1 FROM ruling_file rf WHERE rf.ruling_id = r.id)
		FROM ruling r WHERE r.id = ANY($1) ORDER BY array_position($1, r.id)`, ids)
	if err != nil {
		return out, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var title string
		var court, decided, text *string
		var file bool
		if err := rows.Scan(&id, &title, &court, &decided, &text, &file); err != nil {
			return out, 0, err
		}
		h := obj{{"id", id}, {"title", title}, {"url", Base + "/ruling/" + itoa(id) + "/"}}
		h.nonEmpty("court", court)
		h.nonEmpty("decided_on", decided)
		if text != nil && *text != "" {
			h.set("snippet", snippet(*text))
		}
		if file {
			h.set("pdf_url", pdfURL(id))
		}
		out = append(out, h)
	}
	return out, full, rows.Err()
}

// likeEscape escapes LIKE's wildcards in s.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
