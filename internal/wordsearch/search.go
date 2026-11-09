package wordsearch

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Hit is one text found, in rank order.
type Hit struct {
	ID       int  // the law's or the ruling's id; for an article, its law's
	Position int  // an article's position in its law
	Full     bool // it holds every word of the question
	Phrase   bool // it holds the question as a phrase
	Code     bool // it is (an article of) one of the major codes
	Key      Key  // where the page after it starts
}

// Key is a hit's place in the ranking: the texts holding more of the
// question's words first, then the codes, then (articles) the texts whose
// title holds the question, then the score, then the id. A page starts after
// a key, so a page costs the same at any depth.
type Key struct {
	Units    int
	Code     bool
	Titled   bool
	Score    float64
	Tie      int // the newest first among equals: a law's issue date, a ruling's decision date, in days
	ID       int
	Position int
}

// String is the key as text, for a cursor.
func (k Key) String() string {
	return strings.Join([]string{strconv.Itoa(k.Units), b01(k.Code), b01(k.Titled),
		strconv.FormatFloat(k.Score, 'g', -1, 64), strconv.Itoa(k.Tie), strconv.Itoa(k.ID), strconv.Itoa(k.Position)}, "~")
}

// ParseKey reads a key String wrote; false when s is not one.
func ParseKey(s string) (*Key, bool) {
	f := strings.Split(s, "~")
	if len(f) != 7 || (f[1] != "0" && f[1] != "1") || (f[2] != "0" && f[2] != "1") {
		return nil, false
	}
	var k Key
	var err error
	if k.Units, err = strconv.Atoi(f[0]); err != nil || k.Units < 0 {
		return nil, false
	}
	if k.Score, err = strconv.ParseFloat(f[3], 64); err != nil || math.IsNaN(k.Score) || math.IsInf(k.Score, 0) {
		return nil, false
	}
	// dates in days, ids and positions are Postgres integers
	tie, err := strconv.ParseInt(f[4], 10, 32)
	if err != nil {
		return nil, false
	}
	id, err := strconv.ParseInt(f[5], 10, 32)
	if err != nil {
		return nil, false
	}
	pos, err := strconv.ParseInt(f[6], 10, 32)
	if err != nil {
		return nil, false
	}
	k.Tie, k.ID, k.Position = int(tie), int(id), int(pos)
	k.Code, k.Titled = f[1] == "1", f[2] == "1"
	return &k, true
}

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// Page is one page of hits.
type Page struct {
	Hits  []Hit
	Total int  // candidates in all: texts holding every word, or the rarest words when none does
	Full  int  // how many of them hold every word
	More  bool // more hits follow the last
}

// LawQuery asks for laws by the words of their titles.
type LawQuery struct {
	Text    string
	Terms   []Term // the question's terms, when not plainly Text's words (Terms)
	Full    bool   // only the texts holding every term
	Least   int    // only the texts holding at least this many terms
	English bool   // the unofficial English titles, not the Arabic
	// Except leaves out, from the English titles, the texts whose Arabic
	// title holds every one of these terms: those an Arabic search listed.
	Except []Term
	Year   int    // only texts issued that year
	Number string // only texts with this number; alone, every text with it
	Limit  int
	After  *Key
}

// ArticleQuery asks for articles by the words of their text.
type ArticleQuery struct {
	Text     string
	Full     bool // only the articles holding every word
	Fallback bool // the articles holding some of the words only when none holds every word
	French   bool // the French texts, not the Arabic
	LawID    int  // only this text's articles
	Titled   bool // the articles of the texts whose title holds the question first
	Limit    int
	After    *Key
}

// RulingQuery asks for court rulings by the words of their title and text.
type RulingQuery struct {
	Text     string
	Full     bool // only the rulings holding every word
	Fallback bool // the rulings holding some of the words only when none holds every word
	Limit    int
	After    *Key
}

// fullOnly says a search keeps only the texts holding every word: a question
// in Latin script searched in an Arabic index, where a partial match is a
// Latin name inside an Arabic title ("European Money Management" for "money
// laundering"), not an answer.
func fullOnly(q string, arabicIndex bool) bool { return arabicIndex && !hasArabic(q) }

// Laws are the laws whose titles hold the question's words, best first:
// every word, then the codes, then BM25 (a short title holding the words
// before a long one), then the newest id.
func Laws(ctx context.Context, db Querier, q LawQuery) (Page, error) {
	idx := titleIndexes
	src := source{from: "legislation l", keys: "l.id AS id", names: "id", length: "length(l.title)", perRow: true}
	if q.English {
		idx = titleEnIndexes
		src = source{from: "legislation_title_en te JOIN legislation l ON l.id = te.law_id", keys: "te.law_id AS id", names: "id", length: "length(te.title_en)", perRow: true}
	}
	terms := q.Terms
	if terms == nil {
		terms = Terms(q.Text)
	}
	p, err := planWords(ctx, db, terms, idx)
	if err != nil {
		return Page{}, err
	}
	var params []any
	var where []string
	if q.Year > 0 {
		params = append(params, q.Year)
		where = append(where, fmt.Sprintf("l.issued_on >= make_date($%d, 1, 1) AND l.issued_on < make_date($%d + 1, 1, 1)", len(params), len(params)))
	}
	if q.Number != "" {
		params = append(params, q.Number)
		where = append(where, fmt.Sprintf("l.number = $%d", len(params)))
	}
	if q.English && len(q.Except) > 0 {
		ap, err := planWords(ctx, db, q.Except, titleIndexes)
		if err != nil {
			return Page{}, err
		}
		if c := ap.all(titleIndexes, &params); c != "" {
			where = append(where, "NOT ("+c+")")
		}
	}
	filter := strings.Join(where, " AND ")
	var cands string
	switch {
	case !p.empty():
		cands = candidates(p, idx, src, filter, &params)
	case q.Number != "": // a number alone: "law 44 of 2015"
		cands = fmt.Sprintf(`SELECT l.id, 0 AS units, 0::float8 AS score FROM legislation l WHERE %s`, filter)
	default:
		return Page{}, nil
	}
	params = append(params, q.Text)
	textArg := fmt.Sprintf("$%d", len(params))
	phrase, join := idx[0].phrase(textArg), "JOIN legislation l ON l.id = p.id"
	if q.English {
		join += " JOIN legislation_title_en te ON te.law_id = p.id"
	}
	least := q.Least
	if q.Full || fullOnly(q.Text, !q.English) {
		least = p.units()
	}
	return page(ctx, db, pageSQL{
		cands: cands, units: p.units(), least: least,
		code:   "EXISTS (SELECT 1 FROM code c WHERE c.law_id = g.id)",
		served: "JOIN legislation tl ON tl.id = g.id", tie: "coalesce(tl.issued_on - date '0001-01-01', 0)",
		keys: []string{"id"}, idDesc: true,
		join: join, phrase: phrase,
	}, q.Limit, q.After, params)
}

// TitleLaws are the texts whose Arabic title holds every word of the
// question that a title can hold, at most 20: their articles rank first
// among the rest ("حماية المستهلك" is the Consumer Protection Law's).
func TitleLaws(ctx context.Context, db Querier, text string) ([]int32, error) {
	p, err := planWords(ctx, db, Terms(text), titleIndexes)
	if err != nil || p.words == 0 {
		return nil, err
	}
	var params []any
	cands := candidates(p, titleIndexes, source{from: "legislation l", keys: "l.id AS id", names: "id", length: "length(l.title)"}, "", &params)
	rows, err := db.Query(ctx, `SELECT id FROM (`+cands+`) g WHERE units = `+strconv.Itoa(p.words)+` ORDER BY score DESC LIMIT 20`, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Articles are the articles that hold the question's words, best first:
// every word, then the major codes, then the texts whose title holds the
// question (with Titled), then BM25 with the stored text length. Only texts
// of a kind the site serves.
func Articles(ctx context.Context, db Querier, q ArticleQuery) (Page, error) {
	idx, length := articleIndexes, "a.text_len"
	if q.French {
		idx, length = articleFrIndexes, "length(coalesce(a.text_fr, ''))"
	}
	p, err := planWords(ctx, db, Terms(q.Text), idx)
	if err != nil || p.empty() {
		return Page{}, err
	}
	var titles []int32
	if q.Titled && !q.French && q.LawID == 0 {
		if titles, err = TitleLaws(ctx, db, q.Text); err != nil {
			return Page{}, err
		}
	}
	var params []any
	filter := ""
	if q.LawID != 0 {
		params = append(params, q.LawID)
		filter = fmt.Sprintf("a.law_id = $%d", len(params))
	}
	cands := candidates(p, idx, source{from: "legislation_article a", keys: "a.law_id AS law_id, a.position AS position", names: "law_id, position", length: length}, filter, &params)
	params = append(params, titles)
	titled := fmt.Sprintf("coalesce(g.law_id = ANY($%d::int[]), false)", len(params))
	params = append(params, q.Text)
	least := 0
	if q.Full || fullOnly(q.Text, !q.French) {
		least = p.units()
	}
	return page(ctx, db, pageSQL{
		cands: cands, units: p.units(), least: least, fallback: q.Fallback,
		served: "JOIN legislation l ON l.id = g.law_id JOIN legislation_kind k ON k.name_ar = l.kind",
		code:   "EXISTS (SELECT 1 FROM code c WHERE c.law_id = g.law_id)", titled: titled,
		keys:   []string{"law_id", "position"},
		join:   "JOIN legislation_article a ON a.law_id = p.id AND a.position = p.position",
		phrase: idx[0].phrase(fmt.Sprintf("$%d", len(params))),
	}, q.Limit, q.After, params)
}

// Rulings are the rulings whose title or text holds the question's words,
// best first as Laws.
func Rulings(ctx context.Context, db Querier, q RulingQuery) (Page, error) {
	p, err := planWords(ctx, db, Terms(q.Text), rulingIndexes)
	if err != nil || p.empty() {
		return Page{}, err
	}
	var params []any
	cands := candidates(p, rulingIndexes, source{from: "ruling r", keys: "r.id AS id", names: "id", length: "r.text_len"}, "", &params)
	params = append(params, q.Text)
	least := 0
	if q.Full || fullOnly(q.Text, true) {
		least = p.units()
	}
	return page(ctx, db, pageSQL{
		cands: cands, units: p.units(), least: least, fallback: q.Fallback,
		served: "JOIN ruling tr ON tr.id = g.id", tie: "coalesce(tr.decided_on - date '0001-01-01', 0)",
		keys: []string{"id"}, idDesc: true,
		join: "JOIN ruling r ON r.id = p.id", phrase: rulingIndexes[0].phrase(fmt.Sprintf("$%d", len(params))),
	}, q.Limit, q.After, params)
}

// Judgments are the rulings whose scanned judgment, read by machine, holds
// every word of the question on one page, best first; Hit.Position is that
// page. It leaves out the rulings Rulings finds for the same question (every
// one of them holds its rarest word in the title or summary): a judgment is a
// machine reading of a scan and may hold errors, so it ranks below them.
func Judgments(ctx context.Context, db Querier, q RulingQuery) (Page, error) {
	terms := Terms(q.Text)
	p, err := planWords(ctx, db, terms, judgmentIndexes)
	if err != nil || p.empty() {
		return Page{}, err
	}
	var params []any
	filter := ""
	if sp, err := planWords(ctx, db, terms, rulingIndexes); err != nil {
		return Page{}, err
	} else if c := sp.first(rulingIndexes, &params); c != "" {
		filter = "NOT EXISTS (SELECT 1 FROM ruling r WHERE r.id = p.ruling_id AND " + c + ")"
	}
	cands := candidates(p, judgmentIndexes, source{from: "ruling_page p", keys: "p.ruling_id AS id, p.n AS position", names: "id", length: "p.text_len"}, filter, &params)
	params = append(params, q.Text)
	return page(ctx, db, pageSQL{
		cands: cands, units: p.units(), least: p.units(),
		served: "JOIN ruling tr ON tr.id = g.id", tie: "coalesce(tr.decided_on - date '0001-01-01', 0)",
		keys: []string{"id", "position"}, idDesc: true,
		join:   "JOIN ruling_page rp ON rp.ruling_id = p.id AND rp.n = p.position",
		phrase: fmt.Sprintf("rp.search_stem @@ phraseto_tsquery('arabic', ar_light(ar_digits($%d)))", len(params)),
	}, q.Limit, q.After, params)
}

// pageSQL is one kind's ranking, for page.
type pageSQL struct {
	cands    string   // the candidates (candidates): keys, units, score
	units    int      // units of a text holding every word
	least    int      // keep only the texts holding at least this many
	fallback bool     // the texts holding fewer words than units only when none holds them all
	served   string   // joins that keep only texts the site serves, over g
	code     string   // SQL over g: a major code's; "" for none
	titled   string   // SQL over g: a text whose title holds the question; "" for none
	tie      string   // SQL over g and served: the newest first among equals; "" for none
	keys     []string // the candidates' key columns
	idDesc   bool     // the highest id first among equals
	join     string   // joins over p for phrase
	phrase   string   // SQL: the text holds the question as a phrase
}

// page reads one page of a ranking: the hits after after, at most limit,
// with the candidates' counts.
func page(ctx context.Context, db Querier, s pageSQL, limit int, after *Key, params []any) (Page, error) {
	if limit <= 0 {
		limit = 10
	}
	code, titled, tie := s.code, s.titled, s.tie
	if code == "" {
		code = "false"
	}
	if tie == "" {
		tie = "0"
	}
	if titled == "" {
		titled = "false"
	}
	least := strconv.Itoa(s.least)
	if s.fallback && s.units > s.least {
		// every word when any text holds them all (a one-time filter)
		least = fmt.Sprintf("CASE WHEN EXISTS (SELECT 1 FROM g WHERE units >= %d) THEN %d ELSE %d END", s.units, s.units, s.least)
	}
	id, pos := "r."+s.keys[0], "0"
	if len(s.keys) > 1 {
		pos = "r." + s.keys[1]
	}
	idDir := ""
	if s.idDesc {
		idDir = " DESC"
	}
	order := "r.units DESC, r.code DESC, r.titled DESC, r.score DESC, r.tie DESC, " + id + idDir
	if pos != "0" {
		order += ", " + pos
	}
	cond := "true"
	if after != nil {
		cols := []keyCol{
			{"r.units", true, after.Units}, {"r.code", true, after.Code}, {"r.titled", true, after.Titled},
			{"r.score", true, after.Score}, {"r.tie", true, after.Tie}, {id, s.idDesc, after.ID},
		}
		if pos != "0" {
			cols = append(cols, keyCol{pos, false, after.Position})
		}
		cond = afterCond(cols, &params)
	}
	params = append(params, limit+1)
	// n is MATERIALIZED: inlined, Postgres counted every candidate again for
	// each returned row (9 Nov 2026: "العمل", 38,269 articles, 224 to 114 ms).
	sql := `WITH g AS (` + s.cands + `),
		r AS (SELECT g.*, ` + code + ` AS code, ` + titled + ` AS titled, (` + tie + `)::int AS tie FROM g ` + s.served + ` WHERE g.units >= ` + least + `),
		n AS MATERIALIZED (SELECT count(*) AS total, count(*) FILTER (WHERE units >= ` + strconv.Itoa(s.units) + ` AND ` + strconv.Itoa(s.units) + ` > 0) AS full FROM r)
		SELECT p.id, p.position, p.units, p.code, p.titled, p.score, p.tie, (` + s.phrase + `), n.total, n.full
		FROM (SELECT ` + id + ` AS id, ` + pos + ` AS position, r.units, r.code, r.titled, r.score, r.tie FROM r
			WHERE ` + cond + ` ORDER BY ` + order + ` LIMIT $` + strconv.Itoa(len(params)) + `) p
		` + s.join + ` CROSS JOIN n
		ORDER BY p.units DESC, p.code DESC, p.titled DESC, p.score DESC, p.tie DESC, p.id` + idDir + `, p.position`
	rows, err := db.Query(ctx, sql, params...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	var out Page
	for rows.Next() {
		var h Hit
		var total, full int64
		if err := rows.Scan(&h.ID, &h.Position, &h.Key.Units, &h.Code, &h.Key.Titled, &h.Key.Score, &h.Key.Tie, &h.Phrase, &total, &full); err != nil {
			return Page{}, err
		}
		h.Full = s.units > 0 && h.Key.Units >= s.units
		h.Key.Code, h.Key.ID, h.Key.Position = h.Code, h.ID, h.Position
		out.Total, out.Full = int(total), int(full)
		out.Hits = append(out.Hits, h)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(out.Hits) > limit {
		out.Hits, out.More = out.Hits[:limit], true
	}
	if len(out.Hits) == 0 && after == nil {
		out.Total, out.Full = 0, 0
	}
	return out, nil
}

// keyCol is one column of a keyset, its direction, and the value to start after.
type keyCol struct {
	expr string
	desc bool
	val  any
}

// afterCond is SQL for the rows after a key in the order of cols:
// (c1 beyond v1) OR (c1 = v1 AND ((c2 beyond v2) OR (c2 = v2 AND ...))).
func afterCond(cols []keyCol, params *[]any) string {
	cond := "false"
	for i := len(cols) - 1; i >= 0; i-- {
		c := cols[i]
		*params = append(*params, c.val)
		arg := fmt.Sprintf("$%d", len(*params))
		switch c.val.(type) {
		case bool:
			arg += "::boolean"
		case float64:
			arg += "::float8"
		case int:
			arg += "::int"
		}
		op := ">"
		if c.desc {
			op = "<"
		}
		cond = fmt.Sprintf("(%s %s %s OR (%s = %s AND %s))", c.expr, op, arg, c.expr, arg, cond)
	}
	return cond
}
