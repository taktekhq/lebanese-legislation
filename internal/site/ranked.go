package site

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/taktekhq/lebanese-legislation/internal/wordsearch"
)

// The search page and /api/v1/search/ rank laws, articles and rulings with
// internal/wordsearch, as the MCP server does: one query per kind of text,
// from the indexes and the word counts the build makes (db/indexes.sql). It
// replaced (7 Nov 2026) a search that ranked every match with ts_rank and
// similarity before it kept ten: 2.5 to 4 s on the container for a common
// word. These read its hits into the page's rows.

// titleTerms are a question's words as terms of a title search: a word of one
// or two digits is the text's number; a year may also be the issue year; a
// longer number the text's number or a number its title cites ("11244" is
// decree 11244/2003, then the decrees amending it); a kind word may be the
// text's kind, which its title seldom says ("قانون العمل" is the labour law,
// whose title is «قانون العمل» but a decree's is not «مرسوم ...»); a word with
// synonyms matches any of them.
func titleTerms(words []string) []wordsearch.Term {
	var out []wordsearch.Term
	for i := 0; i < len(words); i++ {
		w := words[i]
		if titleStop[w] {
			continue
		}
		if n, ok := shortNumber(w); ok {
			out = append(out, wordsearch.Term{Or: []string{"l.number_int = $1"}, Args: []any{n}})
			continue
		}
		if y, err := strconv.Atoi(w); err == nil && y >= 1800 && y <= 2100 {
			out = append(out, wordsearch.Term{Text: w, Or: []string{"(l.issued_on >= make_date($1, 1, 1) AND l.issued_on < make_date($1 + 1, 1, 1))"}, Args: []any{y}})
			continue
		}
		if n, err := strconv.Atoi(w); err == nil && n > 0 && len(w) <= 5 {
			out = append(out, wordsearch.Term{Text: w, Or: []string{"l.number_int = $1"}, Args: []any{n}})
			continue
		}
		if kind, ok := titleKinds[w]; ok {
			if one, plural := pluralKinds[kind]; plural {
				out = append(out, wordsearch.Term{Or: []string{"l.kind = ANY($1)"}, Args: []any{kindGroups[one]}, Keep: true})
				continue
			}
			if i+1 < len(words) && (strings.HasPrefix(arNormGo(words[i+1]), "اشتراع") || strings.HasPrefix(arNormGo(words[i+1]), "الاشتراع")) {
				i++ // "مرسوم اشتراعي": the kind only
				out = append(out, wordsearch.Term{Or: []string{"l.kind = ANY($1)"}, Args: []any{kindGroups["decreelaw"]}, Keep: true})
				continue
			}
			out = append(out, wordsearch.Term{Text: w, Or: []string{"l.kind = ANY($1)"}, Args: []any{kindGroups[kind]}, Keep: true})
			continue
		}
		out = append(out, wordsearch.Term{Text: w, Alts: titleSynonyms[arNormGo(w)]})
	}
	return out
}

// lawRows reads the laws of ids, in that order.
func (s *Server) lawRows(ctx context.Context, ids []int) ([]LawRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE l.id = ANY($1) ORDER BY array_position($1, l.id)`, ids)
	if err != nil {
		return nil, err
	}
	return scanLawRows(rows)
}

func hitIDs(hits []wordsearch.Hit) []int {
	ids := make([]int, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	return ids
}

// lawHit is a law found by the words of its title, with its place in the ranking.
type lawHit struct {
	LawRow
	key  wordsearch.Key
	full bool
}

// lawHits reads the laws of a page's hits, in their order.
func (s *Server) lawHits(ctx context.Context, pg wordsearch.Page) ([]lawHit, error) {
	laws, err := s.lawRows(ctx, hitIDs(pg.Hits))
	if err != nil {
		return nil, err
	}
	byID := make(map[int]LawRow, len(laws))
	for _, l := range laws {
		byID[l.ID] = l
	}
	out := make([]lawHit, 0, len(pg.Hits))
	for _, h := range pg.Hits {
		if l, ok := byID[h.ID]; ok {
			out = append(out, lawHit{l, h.Key, h.Full})
		}
	}
	return out, nil
}

func lawRowsOf(hits []lawHit) []LawRow {
	out := make([]LawRow, len(hits))
	for i, h := range hits {
		out[i] = h.LawRow
	}
	return out
}

// rankedLaws are the laws whose Arabic titles hold q's words (titleTerms),
// with the page they came from. year and num narrow them; least, when > 0,
// keeps the titles holding that many terms (else every term).
func (s *Server) rankedLaws(ctx context.Context, q string, year int, num string, least, limit int, after *wordsearch.Key) (wordsearch.Page, []lawHit, error) {
	terms := titleTerms(strings.Fields(q))
	if len(terms) == 0 && num == "" {
		return wordsearch.Page{}, nil, nil
	}
	pg, err := wordsearch.Laws(ctx, s.db, wordsearch.LawQuery{Text: q, Terms: terms, Full: least == 0, Least: least,
		Year: year, Number: num, Limit: limit, After: after})
	if err != nil {
		return pg, nil, err
	}
	hits, err := s.lawHits(ctx, pg)
	return pg, hits, err
}

// englishLaws are the laws whose unofficial English title holds every word
// of a Latin-script q, but for those whose Arabic title holds every term of
// except (the Arabic search listed them).
func (s *Server) englishLaws(ctx context.Context, q string, year, limit int, after *wordsearch.Key, except []wordsearch.Term) (wordsearch.Page, []lawHit, error) {
	if hasArabic(q) || len(latinTokens(q)) == 0 {
		return wordsearch.Page{}, nil, nil
	}
	pg, err := wordsearch.Laws(ctx, s.db, wordsearch.LawQuery{Text: q, English: true, Full: true, Year: year, Limit: limit, After: after, Except: except})
	if err != nil {
		return pg, nil, err
	}
	hits, err := s.lawHits(ctx, pg)
	return pg, hits, err
}

// lawsByEnglishTitle is the search page's use of the English titles: the
// first limit laws englishLaws finds, and how many there are. An older
// database without the table finds nothing.
func (s *Server) lawsByEnglishTitle(ctx context.Context, q string, limit int) ([]LawRow, int) {
	pg, hits, err := s.englishLaws(ctx, q, 0, limit, nil, nil)
	if err != nil {
		return nil, 0
	}
	return lawRowsOf(hits), pg.Full
}

// articleHit is an article found by its words: its law's row with Article
// set, its position, and its snippet.
type articleHit struct {
	LawRow
	Position int
	key      wordsearch.Key
}

// rankedArticles are the articles that hold q's words, best first: the
// Arabic texts for a question with Arabic in it, else the French texts
// (every word, so an English question is read into Arabic instead). When no
// Arabic article holds every word, those holding the rarest of them. Among
// the first ones that hold as many words, an exact phrase comes first with
// phrase, for a page no cursor continues (a reordered page cut short would
// lose rows). last is the key of the page's last hit by rank, before that
// reordering: the page after a whole reordered page starts there.
func (s *Server) rankedArticles(ctx context.Context, q string, limit int, after *wordsearch.Key, phrase bool) (pg wordsearch.Page, hits []articleHit, last wordsearch.Key, err error) {
	fr := !hasArabic(q)
	pg, err = wordsearch.Articles(ctx, s.db, wordsearch.ArticleQuery{Text: q, French: fr, Full: fr, Fallback: !fr, Titled: true, Limit: limit, After: after})
	if err != nil || len(pg.Hits) == 0 {
		return pg, nil, last, err
	}
	last = pg.Hits[len(pg.Hits)-1].Key
	if phrase {
		phraseFirst(pg.Hits)
	}
	laws := make([]int32, len(pg.Hits))
	pos := make([]int32, len(pg.Hits))
	for i, h := range pg.Hits {
		laws[i], pos[i] = int32(h.ID), int32(h.Position)
	}
	text, cfg, query := "a.text_ar", "arabic", "websearch_to_tsquery('arabic', ar_light($3))"
	if fr {
		text, cfg, query = "coalesce(a.text_fr, '')", "french", "websearch_to_tsquery('french', $3)"
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+`, a.number, a.position,
		ts_headline('`+cfg+`', coalesce(`+text+`, ''), `+query+`, '`+headlineMarks+`, MaxWords=40, MinWords=18, ShortWord=2')
		FROM unnest($1::int[], $2::int[]) WITH ORDINALITY h(law_id, position, ord)
		JOIN legislation_article a ON a.law_id = h.law_id AND a.position = h.position
		JOIN legislation l ON l.id = a.law_id JOIN legislation_kind k ON k.name_ar = l.kind
		ORDER BY h.ord`, laws, pos, q)
	if err != nil {
		return pg, nil, last, err
	}
	hits, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (articleHit, error) {
		var h articleHit
		var snip *string
		err := row.Scan(append(lawRowDest(&h.LawRow), &h.Article, &h.Position, &snip)...)
		if snip != nil {
			h.Snippet = *snip
		}
		return h, err
	})
	for i := range hits {
		if i < len(pg.Hits) {
			hits[i].key = pg.Hits[i].Key
		}
	}
	return pg, hits, last, err
}

// phraseFirst puts the hits that hold the question as a phrase first among
// those that hold as many words, in the same group (a code, a titled text).
func phraseFirst(hits []wordsearch.Hit) {
	slices.SortStableFunc(hits, func(a, b wordsearch.Hit) int {
		switch {
		case a.Key.Units != b.Key.Units:
			return b.Key.Units - a.Key.Units
		case a.Code != b.Code:
			if a.Code {
				return -1
			}
			return 1
		case a.Key.Titled != b.Key.Titled:
			if a.Key.Titled {
				return -1
			}
			return 1
		case a.Phrase != b.Phrase:
			if a.Phrase {
				return -1
			}
			return 1
		}
		return 0
	})
}

// rulingHit is one ruling found by a search. Page is set when only its
// scanned judgment's machine reading matched: the page the excerpt is from.
type rulingHit struct {
	ID      int
	Title   string
	Court   string
	On      *time.Time
	Snippet string
	Page    int
	HasFile bool
	key     wordsearch.Key
}

// The phases of the rulings part of a search: the titles and summaries,
// then the judgments (rulingsJudgments).
const rulingsJudgments = 2

// rulingsPage is one page of the rulings that hold every word of q: those
// whose title or summary does first, then those only the judgment's machine
// reading does (it may hold errors), from pos (nil: the first page). total
// counts both; next is where the page after it starts (phase 0: none).
func (s *Server) rulingsPage(ctx context.Context, q string, limit int, pos *searchPos) (hits []rulingHit, total int, next searchPos, err error) {
	phase, key := phaseRanked, keyOf(pos)
	if pos != nil {
		phase = pos.phase
	}
	sumLimit := limit
	if phase != phaseRanked {
		sumLimit, key = 1, nil // to count them
	}
	pg, sum, err := s.rulingHits(ctx, q, false, sumLimit, key)
	if err != nil {
		return nil, 0, next, err
	}
	total = pg.Total
	if phase == phaseRanked {
		hits = sum
		if pg.More {
			// The judgments follow on a later page and count from this one:
			// "يوسف كنعان" said 11 rulings while its pages listed 185 (11 Nov
			// 2026). A word in thousands of rulings is not counted further
			// (the page says "1,000+"; counting its judgments took 0.5 to 2 s).
			if total < 1000 {
				jpg, _, err := s.rulingHits(ctx, q, true, 1, nil)
				if err != nil {
					return nil, 0, next, err
				}
				total += jpg.Total
			}
			return hits, total, searchPos{phase: phaseRanked, key: keyField(sum[len(sum)-1].key)}, nil
		}
		phase, key = rulingsJudgments, nil
	} else if phase == rulingsJudgments {
		key = keyOf(pos)
	}
	if phase != rulingsJudgments {
		return hits, total, next, nil
	}
	room := limit - len(hits)
	jpg, judged, err := s.rulingHits(ctx, q, true, max(room, 1), key)
	if err != nil {
		return nil, 0, next, err
	}
	total += jpg.Total
	switch {
	case room <= 0 && len(judged) > 0:
		next = searchPos{phase: rulingsJudgments}
	case room > 0:
		hits = append(hits, judged...)
		if jpg.More {
			next = searchPos{phase: rulingsJudgments, key: keyField(judged[len(judged)-1].key)}
		}
	}
	return hits, total, next, nil
}

// rulingHits reads a page of rulings found by their titles and summaries,
// or (judgments) by their judgments' pages, with their excerpts.
func (s *Server) rulingHits(ctx context.Context, q string, judgments bool, limit int, after *wordsearch.Key) (wordsearch.Page, []rulingHit, error) {
	find := wordsearch.Rulings
	if judgments {
		find = wordsearch.Judgments
	}
	pg, err := find(ctx, s.db, wordsearch.RulingQuery{Text: q, Full: true, Limit: limit, After: after})
	if err != nil || len(pg.Hits) == 0 {
		return pg, nil, err
	}
	ids := hitIDs(pg.Hits)
	pages := make([]int, len(pg.Hits))
	for i, h := range pg.Hits {
		pages[i] = h.Position
	}
	const opts = `'` + headlineMarks + `, MaxWords=35, MinWords=15, ShortWord=2'`
	rows, err := s.db.Query(ctx, `SELECT r.id, r.title, coalesce(r.court, ''), r.decided_on, h.pg,
		ts_headline('arabic', CASE WHEN h.pg = 0 THEN coalesce(r.text_ar, '')
			ELSE (SELECT text_ar FROM ruling_page WHERE ruling_id = r.id AND n = h.pg) END,
			websearch_to_tsquery('arabic', ar_light(ar_digits($3))), `+opts+`),
		EXISTS (SELECT 1 FROM ruling_file rf WHERE rf.ruling_id = r.id)
		FROM unnest($1::int[], $2::int[]) WITH ORDINALITY h(id, pg, ord) JOIN ruling r ON r.id = h.id
		ORDER BY h.ord`, ids, pages, q)
	if err != nil {
		return pg, nil, err
	}
	hits, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (rulingHit, error) {
		var h rulingHit
		var snip *string
		err := row.Scan(&h.ID, &h.Title, &h.Court, &h.On, &h.Page, &snip, &h.HasFile)
		if snip != nil {
			h.Snippet = *snip
		}
		return h, err
	})
	for i := range hits {
		if i < len(pg.Hits) {
			hits[i].key = pg.Hits[i].Key
		}
	}
	return pg, hits, err
}

// keyOf reads a search part's engine key from a cursor; nil from the start.
func keyOf(pos *searchPos) *wordsearch.Key {
	if pos == nil || len(pos.key) == 0 {
		return nil
	}
	k, _ := wordsearch.ParseKey(pos.key[0])
	return k
}

func keyField(k wordsearch.Key) []string { return []string{k.String()} }
