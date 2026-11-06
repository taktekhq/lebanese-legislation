package mcpserver

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Search by words, scored the way search engines score it, in Postgres alone.
//
// Each word of the question is looked up in a GIN index (db/indexes.sql) and
// weighted by how rare it is: search_term, built with the database, holds how
// many texts each word is in, so nothing is counted at search time. A text's
// score is the sum of the weights of the words it holds. Texts that hold every
// word come first; then the ones missing the commonest words, which cost the
// least (Meilisearch's "frequency" matching strategy). That is one query, not
// a chain of retries. Only the top candidates are read in full, to rank an
// exact phrase and the codes first.
//
// It replaced (6 Nov 2026) an FTS5 port that scored every candidate in Go and,
// when no text held every word, retried up to a dozen times, one search after
// another: a question that matched nothing took 2 to 5 s on the container.
// The same Arabic question took 848 ms read in full and 37 ms from the index.

// ftsIndex is a full-text index the search scores against: a tsvector column
// or expression with a GIN index, and its rows in search_term.
type ftsIndex struct {
	corpus string  // search_term.corpus
	vector string  // the indexed tsvector, over the query's alias
	config string  // its text search configuration
	norm   string  // how its text is normalized before it is parsed; %s is the text
	weight float64 // a stem matches more loosely than the word as written
}

// lexemes is SQL for the lexemes this index makes of the text arg.
func (x ftsIndex) lexemes(arg string) string {
	return fmt.Sprintf("tsvector_to_array(to_tsvector('%s', %s))", x.config, fmt.Sprintf(x.norm, arg))
}

var (
	articleIndexes = []ftsIndex{
		{"article_exact", "a.search_exact", "simple", "ar_norm(%s)", 1},
		{"article_stem", "a.search_stem", "arabic", "ar_light(%s)", stemWeight},
	}
	articleFrIndexes = []ftsIndex{{"article_fr", "a.search_fr", "french", "fr_fold(%s)", 1}}
	rulingIndexes    = []ftsIndex{
		{"ruling_exact", "r.search_exact", "simple", "ar_norm(%s)", 1},
		{"ruling_stem", "r.search_stem", "arabic", "ar_light(%s)", stemWeight},
	}
	titleIndexes = []ftsIndex{
		{"law_title", "to_tsvector('simple', ar_norm(l.title))", "simple", "ar_norm(%s)", 1},
		{"law_title_stem", "to_tsvector('arabic', ar_light(l.title))", "arabic", "ar_light(%s)", stemWeight},
	}
	titleEnIndexes = []ftsIndex{{"law_title_en", "to_tsvector('english', te.title_en)", "english", "%s", 1}}
)

// stemWeight is what a match on a word's stem counts against the word as
// written: Arabic joins its prepositions to the word ("بالشفعة" is "ب" and
// "الشفعة"), so the stem is often the only match there is.
const stemWeight = 0.8

const (
	// commonShare: a word in more than this share of an index's texts tells
	// almost nothing ("في" is in 56% of the articles); it is not looked up,
	// unless every word of the question is that common.
	commonShare = 0.2
	maxWords    = 8  // the rarest words of a long question
	topRanked   = 50 // candidates read in full to rank phrases and codes
)

// lookup is one word looked up in one index, with its weight.
type lookup struct {
	word   int
	index  int
	lexeme string
	weight float64
}

// wordPlan is how a question is looked up.
type wordPlan struct {
	lookups []lookup
	words   int      // words with at least one lookup: a text holding all of them is a full match
	order   []int    // those words, the first a text keeps longest: the rarest, a word no text holds last
	common  []string // every word was common: their lexemes in the first index, all required
	weight  float64  // the common words' summed weight, when common is used
	avgLen  float64  // the first index's average text length (search_term '#len')
}

func (p wordPlan) empty() bool { return len(p.lookups) == 0 && len(p.common) == 0 }

// wordForm is one way a word of the question is looked up.
type wordForm struct {
	text   string
	word   int     // the question's word it is a form of
	weight float64 // what a match on it counts against the word as written
}

// wordForms are the forms each word is looked up in: as written and, for an
// Arabic word, without a leading "و" or "ف" ("and", "so"). The Arabic
// stemmer keeps them before an alef ("والخامسة" stems to "والخامس"), so
// "والخامسة" would find only the texts that write "والخامسة". The word as
// written stays a form: "وكالة" is not "و" and "كالة".
func wordForms(words []string) []wordForm {
	var out []wordForm
	for i, w := range words {
		out = append(out, wordForm{w, i, 1})
		n := arNorm(w)
		for _, p := range []string{"و", "ف"} {
			if strings.HasPrefix(n, p) && runeLen(n)-1 >= 3 && arabicChar.MatchString(n) {
				out = append(out, wordForm{n[len(p):], i, stemWeight})
				break
			}
		}
	}
	return out
}

// planWords reads each word's lexemes in each index and their rarity from
// search_term, in one query.
func (s *Server) planWords(ctx context.Context, words []string, idx []ftsIndex) (wordPlan, error) {
	var p wordPlan
	if len(words) == 0 {
		return p, nil
	}
	forms := wordForms(words)
	texts := make([]string, len(forms))
	for i, f := range forms {
		texts[i] = f.text
	}
	var parts []string
	for i, x := range idx {
		parts = append(parts, fmt.Sprintf(`SELECT %d AS idx, w.ord::int - 1 AS form, l AS lexeme, coalesce(st.docs, 0) AS docs,
			coalesce((SELECT docs FROM search_term WHERE corpus = '%s' AND term = ''), 0) AS n
			FROM unnest($1::text[]) WITH ORDINALITY w(word, ord)
			CROSS JOIN LATERAL unnest(%s) l
			LEFT JOIN search_term st ON st.corpus = '%s' AND st.term = l`, i, x.corpus, x.lexemes("w.word"), x.corpus))
	}
	parts = append(parts, fmt.Sprintf(`SELECT -1, -1, '', coalesce((SELECT docs FROM search_term WHERE corpus = '%s' AND term = '#len'), 0), 0`, idx[0].corpus))
	rows, err := s.db.Query(ctx, strings.Join(parts, " UNION ALL "), texts)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	type cand struct {
		lookup
		docs    float64
		common  bool
		written bool // the word as written, not another form
	}
	var all []cand
	for rows.Next() {
		var c cand
		var form int
		var n float64
		if err := rows.Scan(&c.index, &form, &c.lexeme, &c.docs, &n); err != nil {
			return p, err
		}
		if c.index < 0 {
			p.avgLen = c.docs
			continue
		}
		c.word, c.written = forms[form].word, forms[form].weight == 1
		// BM25's idf
		n = math.Max(n, 1)
		c.weight = math.Log(1+(n-c.docs+0.5)/(c.docs+0.5)) * idx[c.index].weight * forms[form].weight
		c.common = n > 100 && c.docs > commonShare*n
		all = append(all, c)
	}
	if err := rows.Err(); err != nil {
		return p, err
	}
	// Each word's weight is its best lookup's. A word no text holds (one
	// search_term does not know) has the highest weight of all, but every
	// text it is required in is none: it goes last, the first set aside.
	best := map[int]float64{}
	held := map[int]bool{}
	for _, c := range all {
		if !c.common && c.weight > best[c.word] {
			best[c.word] = c.weight
		}
		if !c.common && c.docs > 0 {
			held[c.word] = true
		}
	}
	var kept []int
	for w := range best {
		kept = append(kept, w)
	}
	sort.Slice(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if held[a] != held[b] {
			return held[a]
		}
		return best[a] > best[b] || best[a] == best[b] && a < b
	})
	if len(kept) > maxWords {
		kept = kept[:maxWords]
	}
	keep := setOfInts(kept...)
	at := map[string]int{} // a word's lexeme in an index once, at its best weight
	for _, c := range all {
		if c.common || !keep[c.word] {
			continue
		}
		k := fmt.Sprint(c.word, " ", c.index, " ", c.lexeme)
		if i, seen := at[k]; seen {
			p.lookups[i].weight = math.Max(p.lookups[i].weight, c.weight)
			continue
		}
		at[k] = len(p.lookups)
		p.lookups = append(p.lookups, c.lookup)
	}
	p.words, p.order = len(kept), kept
	if p.words == 0 {
		seen := map[string]bool{}
		for _, c := range all {
			if c.index == 0 && c.written && !seen[c.lexeme] {
				seen[c.lexeme] = true
				p.common = append(p.common, c.lexeme)
				p.weight += c.weight
			}
		}
	}
	return p, nil
}

func setOfInts(xs ...int) map[int]bool {
	m := make(map[int]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// kindSQL is what a search reads candidates from.
type kindSQL struct {
	from   string // the FROM clause
	keys   string // the key columns, as "expr AS name"
	names  string // their names, comma separated
	length string // the text's length, over from (a stored column where the text is long)
}

// BM25's constants. Each word counts once: the index says whether a text
// holds a word, not how often, so the score is the words' summed rarity
// scaled by the text's length against the average (a match in a short text
// is worth more).
const bm25K1, bm25B = 1.2, 0.75

// grouped is SQL for every candidate of a plan: its keys (names), units (how
// many of the question's words it holds) and score, from the rows of k.from
// that pass filter. Its parameters are appended to params.
func grouped(p wordPlan, idx []ftsIndex, k kindSQL, filter string, params *[]any) string {
	and := ""
	if filter != "" {
		and = " AND " + filter
	}
	avg := math.Max(p.avgLen, 1)
	norm := func(length string) string {
		return fmt.Sprintf("%g / (1 + %g * (%g + %g * %s / %g))", bm25K1+1, bm25K1, 1-bm25B, bm25B, length, avg)
	}
	if len(p.lookups) == 0 { // every word common: the texts holding all of them, the shortest first
		var qs []string
		for _, l := range p.common {
			*params = append(*params, tsLexeme(l))
			qs = append(qs, fmt.Sprintf("$%d::tsquery", len(*params)))
		}
		return fmt.Sprintf(`SELECT %s, %d AS units, %g * %s AS score FROM %s WHERE %s @@ (%s)%s ORDER BY %s LIMIT 5000`,
			k.keys, len(p.common), p.weight, norm(k.length), k.from, idx[0].vector, strings.Join(qs, " && "), and, k.length)
	}
	// One parameter per lookup, shared by both buckets.
	arg := make([]int, len(p.lookups))
	for i, l := range p.lookups {
		*params = append(*params, tsLexeme(l.lexeme))
		arg[i] = len(*params)
	}
	// Each word: any of its lookups, and its weight (the best of them).
	byWord := map[int][]string{}
	best := map[int]float64{}
	for i, l := range p.lookups {
		byWord[l.word] = append(byWord[l.word], fmt.Sprintf("%s @@ $%d::tsquery", idx[l.index].vector, arg[i]))
		best[l.word] = math.Max(best[l.word], l.weight)
	}
	order := p.order
	// Buckets, Meilisearch's "frequency" strategy in one statement: the texts
	// holding every word, then those holding all but the commonest word, then
	// all but the two commonest, down to the rarest word alone. Each bucket
	// is an intersection of index bitmaps; a text is kept in the best bucket
	// it is in, and scored by the summed rarity of that bucket's words scaled
	// by its length.
	// A bucket is read only while the buckets above it hold fewer than
	// topRanked texts: Postgres checks that once (a one-time filter) and then
	// skips the scan, so a question that many texts match word for word costs
	// one intersection.
	var ctes, names []string
	for n := len(order); n >= 1; n-- {
		var conds []string
		sum := 0.0
		for _, w := range order[:n] {
			conds = append(conds, "("+strings.Join(byWord[w], " OR ")+")")
			sum += best[w]
		}
		enough := ""
		if len(names) > 0 {
			var above []string
			for _, nm := range names {
				above = append(above, "SELECT 1 FROM "+nm)
			}
			enough = fmt.Sprintf(" AND (SELECT count(*) FROM (%s LIMIT %d) f) < %d", strings.Join(above, " UNION ALL "), topRanked, topRanked)
		}
		name := fmt.Sprintf("bucket%d", n)
		ctes = append(ctes, fmt.Sprintf(`%s AS MATERIALIZED (SELECT %s, %d AS units, %g * %s AS score FROM %s WHERE %s%s%s)`,
			name, k.keys, n, sum, norm(k.length), k.from, strings.Join(conds, " AND "), and, enough))
		names = append(names, name)
	}
	var all []string
	for _, nm := range names {
		all = append(all, "SELECT * FROM "+nm)
	}
	return fmt.Sprintf(`WITH %s SELECT DISTINCT ON (%s) * FROM (%s) b ORDER BY %s, units DESC`,
		strings.Join(ctes, ", "), k.names, strings.Join(all, " UNION ALL "), k.names)
}

// queryWords are the words of a question a search looks up.
func queryWords(q string) []string {
	var out []string
	for _, w := range jsFields(q) {
		if w = trimEdges(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// fullOnly says a search keeps only the texts holding every word: a question
// in Latin script searched in an Arabic index, where a partial match is a
// Latin name inside an Arabic title ("European Money Management" for "money
// laundering"), not an answer.
func fullOnly(q string, arabicIndex bool) bool { return arabicIndex && !hasArabic(q) }

// rankedLaws are the laws whose titles (Arabic, or the unofficial English
// with en) hold the question's words, best first: every word, then the codes,
// then BM25 (a short title holding the words before a long one), then an
// exact phrase, then the newest. full is how many of them hold every word.
func (s *Server) rankedLaws(ctx context.Context, q string, en bool, year yearVal, num string, limit int) (laws []lawRow, full int, err error) {
	idx := titleIndexes
	k := kindSQL{from: "legislation l", keys: "l.id AS id", names: "id", length: "length(l.title)"}
	if en {
		idx = titleEnIndexes
		k = kindSQL{from: "legislation_title_en te JOIN legislation l ON l.id = te.law_id", keys: "te.law_id AS id", names: "id", length: "length(te.title_en)"}
	}
	p, err := s.planWords(ctx, queryWords(q), idx)
	if err != nil {
		return nil, 0, err
	}
	var params []any
	var where []string
	if year.positive() {
		params = append(params, int(truncf(year.f)))
		where = append(where, fmt.Sprintf("l.issued_on >= make_date($%d, 1, 1) AND l.issued_on < make_date($%d + 1, 1, 1)", len(params), len(params)))
	}
	if num != "" {
		params = append(params, num)
		where = append(where, fmt.Sprintf("l.number = $%d", len(params)))
	}
	filter := strings.Join(where, " AND ")
	var cands string
	units := p.words
	switch {
	case !p.empty():
		cands = grouped(p, idx, k, filter, &params)
	case num != "": // a number alone: "law 44 of 2015"
		cands = fmt.Sprintf(`SELECT l.id, 0 AS units, 0::float8 AS score FROM legislation l WHERE %s`, filter)
	default:
		return nil, 0, nil
	}
	if len(p.lookups) == 0 {
		units = len(p.common)
	}
	minUnits := 0
	if fullOnly(q, !en) {
		minUnits = units
	}
	params = append(params, q, limit)
	phrase := fmt.Sprintf("to_tsvector('simple', ar_norm(l.title)) @@ phraseto_tsquery('simple', ar_norm($%d))", len(params)-1)
	if en {
		phrase = fmt.Sprintf("to_tsvector('english', coalesce(te.title_en, '')) @@ phraseto_tsquery('english', $%d)", len(params)-1)
	}
	// lawJoins already joins legislation_title_en te for the English phrase
	rows, err := s.db.Query(ctx, `WITH g AS (`+cands+`),
		top AS (SELECT g.* FROM g LEFT JOIN code c ON c.law_id = g.id
			WHERE g.units >= `+fmt.Sprint(minUnits)+`
			ORDER BY g.units DESC, (c.law_id IS NOT NULL) DESC, g.score DESC, g.id DESC LIMIT `+fmt.Sprint(topRanked)+`)
		SELECT `+lawCols+`, top.units >= `+fmt.Sprint(units)+` AND `+fmt.Sprint(units)+` > 0 FROM top JOIN legislation l ON l.id = top.id `+lawJoins+`
		ORDER BY top.units DESC, (c.slug IS NOT NULL) DESC, top.score DESC, (`+phrase+`) DESC, l.issued_on DESC NULLS LAST, l.id
		LIMIT $`+fmt.Sprint(len(params)), params...)
	if err != nil {
		return nil, 0, err
	}
	var flags []bool
	laws, err = scanLaws(rows, func() []any {
		flags = append(flags, false)
		return []any{&flags[len(flags)-1]}
	})
	for _, f := range flags {
		if f {
			full++
		}
	}
	return laws, full, err
}

// titleLaws are the texts whose Arabic title holds every word of the
// question that a title can hold: their articles rank first among the rest
// ("حماية المستهلك" is the Consumer Protection Law's).
func (s *Server) titleLaws(ctx context.Context, q string) ([]int32, error) {
	p, err := s.planWords(ctx, queryWords(q), titleIndexes)
	if err != nil || len(p.lookups) == 0 || p.words == 0 {
		return nil, err
	}
	var params []any
	cands := grouped(p, titleIndexes, kindSQL{from: "legislation l", keys: "l.id AS id", names: "id", length: "length(l.title)"}, "", &params)
	rows, err := s.db.Query(ctx, `SELECT id FROM (`+cands+`) g WHERE units = `+fmt.Sprint(p.words)+` ORDER BY score DESC LIMIT 20`, params...)
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

// rankedArticles are the articles that hold the question's words, best
// first: every word, then the major codes, then the texts whose title holds
// the question, then BM25 (with the stored text length), then an exact
// phrase among the top. lawID keeps them to one text; fr searches the French
// texts. Only texts of a kind the server serves.
func (s *Server) rankedArticles(ctx context.Context, q string, fr bool, lawID, limit int) (hits []articleHit, full int, err error) {
	idx := articleIndexes
	length := "a.text_len"
	if fr {
		idx, length = articleFrIndexes, "length(coalesce(a.text_fr, ''))"
	}
	p, err := s.planWords(ctx, queryWords(q), idx)
	if err != nil || p.empty() {
		return nil, 0, err
	}
	var titles []int32
	if !fr && lawID == 0 {
		if titles, err = s.titleLaws(ctx, q); err != nil {
			return nil, 0, err
		}
	}
	var params []any
	filter := ""
	if lawID != 0 {
		params = append(params, lawID)
		filter = fmt.Sprintf("a.law_id = $%d", len(params))
	}
	units := p.words
	if len(p.lookups) == 0 {
		units = len(p.common)
	}
	minUnits := 0
	if fullOnly(q, !fr) {
		minUnits = units
	}
	cands := grouped(p, idx, kindSQL{from: "legislation_article a", keys: "a.law_id AS law_id, a.position AS position", names: "law_id, position", length: length}, filter, &params)
	params = append(params, titles)
	titleArg := len(params)
	params = append(params, q, limit)
	phrase := fmt.Sprintf("%s @@ phraseto_tsquery('%s', %s)", idx[0].vector, idx[0].config, fmt.Sprintf(idx[0].norm, fmt.Sprintf("$%d", len(params)-1)))
	rows, err := s.db.Query(ctx, `WITH g AS (`+cands+`),
		top AS (SELECT g.*, c.law_id IS NOT NULL AS code, g.law_id = ANY($`+fmt.Sprint(titleArg)+`::int[]) AS titled FROM g
			JOIN legislation l ON l.id = g.law_id JOIN legislation_kind k ON k.name_ar = l.kind
			LEFT JOIN code c ON c.law_id = g.law_id
			WHERE g.units >= `+fmt.Sprint(minUnits)+`
			ORDER BY g.units DESC, (c.law_id IS NOT NULL) DESC, titled DESC, g.score DESC, g.law_id, g.position LIMIT `+fmt.Sprint(topRanked)+`)
		SELECT top.law_id, top.position, top.units >= `+fmt.Sprint(units)+` FROM top
		JOIN legislation_article a ON a.law_id = top.law_id AND a.position = top.position
		ORDER BY top.units DESC, top.code DESC, top.titled DESC, (`+phrase+`) DESC, top.score DESC, top.law_id, top.position
		LIMIT $`+fmt.Sprint(len(params)), params...)
	if err != nil {
		return nil, 0, err
	}
	var cs []candidate
	for rows.Next() {
		var c candidate
		var f bool
		if err := rows.Scan(&c.lawID, &c.position, &f); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if f {
			full++
		}
		cs = append(cs, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	col := "a.text_ar"
	if fr {
		col = "a.text_fr"
	}
	hits, err = s.hitsFor(ctx, cs, col)
	return hits, full, err
}

// rankedRulings are the rulings whose title or text holds the question's
// words, best first as rankedLaws (the newest last among equals).
func (s *Server) rankedRulings(ctx context.Context, q string, limit int) (out []any, full int, err error) {
	p, err := s.planWords(ctx, queryWords(q), rulingIndexes)
	if err != nil || p.empty() {
		return []any{}, 0, err
	}
	var params []any
	units := p.words
	if len(p.lookups) == 0 {
		units = len(p.common)
	}
	minUnits := 0
	if fullOnly(q, true) {
		minUnits = units
	}
	cands := grouped(p, rulingIndexes, kindSQL{from: "ruling r", keys: "r.id AS id", names: "id", length: "r.text_len"}, "", &params)
	params = append(params, q, limit)
	rows, err := s.db.Query(ctx, `WITH g AS (`+cands+`),
		top AS (SELECT * FROM g WHERE units >= `+fmt.Sprint(minUnits)+` ORDER BY units DESC, score DESC, id DESC LIMIT `+fmt.Sprint(topRanked)+`)
		SELECT r.id, r.title, r.court, to_char(r.decided_on, 'YYYY-MM-DD'), r.text_ar,
			EXISTS (SELECT 1 FROM ruling_file rf WHERE rf.ruling_id = r.id), top.units >= `+fmt.Sprint(units)+`
		FROM top JOIN ruling r ON r.id = top.id
		ORDER BY top.units DESC, top.score DESC, (r.search_exact @@ phraseto_tsquery('simple', ar_norm($`+fmt.Sprint(len(params)-1)+`))) DESC,
			r.decided_on DESC NULLS LAST, r.id
		LIMIT $`+fmt.Sprint(len(params)), params...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out = []any{}
	for rows.Next() {
		var id int
		var title string
		var court, decided, text *string
		var file, f bool
		if err := rows.Scan(&id, &title, &court, &decided, &text, &file, &f); err != nil {
			return nil, 0, err
		}
		if f {
			full++
		}
		h := obj{{"id", id}, {"title", title}, {"url", fmt.Sprintf("%s/ruling/%d/", Base, id)}}
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

// tsLexeme is a lexeme as a tsquery literal.
func tsLexeme(t string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(t, `\`, `\\`), "'", "''") + "'"
}

// likeEscape escapes LIKE's wildcards in s.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
