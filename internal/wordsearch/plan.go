// Package wordsearch searches the texts by their words, scored the way search
// engines score them, in PostgreSQL alone. The website, the API and the MCP
// server all search with it.
//
// Each word of the question is looked up in a GIN index (db/indexes.sql) and
// weighted by how rare it is: search_term, built with the database, holds how
// many texts each word is in, so nothing is counted at search time. A text's
// score is the sum of the weights of the words it holds (BM25's idf), scaled
// by its length. Texts that hold every word come first; then the ones missing
// the commonest words, which cost the least (Meilisearch's "frequency"
// matching strategy). That is one query per kind of text, not a chain of
// retries, and nothing is loaded into the server's memory.
//
// It replaced (7 Nov 2026) an FTS5 port in the MCP server that scored every
// candidate in Go and, when no text held every word, retried up to a dozen
// times, one search after another (2 to 5 s on the container), and the
// website's own search, which ranked every match with ts_rank and similarity
// before it kept ten (2.5 to 4 s on the container).
package wordsearch

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Querier is what the search reads with: a pool, a connection or a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// index is a full-text index the search scores against: a tsvector column or
// expression with a GIN index, and its rows in search_term.
type index struct {
	corpus string  // search_term.corpus
	vector string  // the indexed tsvector, over the query's alias
	config string  // its text search configuration
	norm   string  // how its text is normalized before it is parsed; %s is the text
	weight float64 // a stem matches more loosely than the word as written
	prefix bool    // the word as the start of a word ("اللبناني" in "اللبنانية"), at least 4 letters
}

// lexemes is SQL for the lexemes this index makes of the text arg.
func (x index) lexemes(arg string) string {
	return fmt.Sprintf("tsvector_to_array(to_tsvector('%s', %s))", x.config, fmt.Sprintf(x.norm, arg))
}

// phrase is SQL that holds when the text holds the question as a phrase; arg
// is the question's parameter.
func (x index) phrase(arg string) string {
	return fmt.Sprintf("%s @@ phraseto_tsquery('%s', %s)", x.vector, x.config, fmt.Sprintf(x.norm, arg))
}

var (
	articleIndexes = []index{
		{"article_exact", "a.search_exact", "simple", "ar_norm(%s)", 1, false},
		{"article_stem", "a.search_stem", "arabic", "ar_light(%s)", stemWeight, false},
	}
	articleFrIndexes = []index{{"article_fr", "a.search_fr", "french", "fr_fold(%s)", 1, false}}
	rulingIndexes    = []index{
		{"ruling_exact", "r.search_exact", "simple", "ar_norm(%s)", 1, false},
		{"ruling_stem", "r.search_stem", "arabic", "ar_light(%s)", stemWeight, false},
	}
	// Titles hold the word as written; then without a leading article
	// (ar_strip: «للبيانات» is «البيانات»); then as the start of a word
	// («اللبناني» in «اللبنانية»), never its stem: a title is a few words, and
	// a stem joins too much («الغابات» and «الغابون», Gabon, stem alike;
	// «الخارجي» is not «الخارجية»).
	titleIndexes = []index{
		{"law_title", "l.title_exact", "simple", "ar_norm(%s)", 1, false},
		{"law_title_base", "l.title_base", "simple", "ar_strip(ar_norm(%s))", 0.9, false},
		{"law_title_base", "l.title_base", "simple", "ar_strip(ar_norm(%s))", stemWeight, true},
	}
	titleEnIndexes = []index{{"law_title_en", "te.search_en", "english", "%s", 1, false}}
	// the machine readings of scanned judgments, page by page
	judgmentIndexes = []index{{"judgment_stem", "p.search_stem", "arabic", "ar_light(ar_digits(%s))", 1, false}}
)

// stemWeight is what a match on a word's stem, or on another form of it,
// counts against the word as written: Arabic joins its prepositions to the
// word ("بالشفعة" is "ب" and "الشفعة"), so the stem is often the only match
// there is.
const stemWeight = 0.8

const (
	// commonShare: a word in more than this share of an index's texts tells
	// almost nothing ("في" is in 56% of the articles); it is not looked up,
	// unless every word of the question is that common.
	commonShare = 0.2
	maxWords    = 8  // the rarest words of a long question
	enough      = 50 // a bucket is read only while the buckets above hold fewer texts than this
)

// lookup is one word looked up in one index, with its weight.
type lookup struct {
	word   int
	index  int
	lexeme string
	weight float64
}

// query is the lookup as a tsquery literal.
func (l lookup) query(idx []index) string {
	if idx[l.index].prefix {
		return tsLexeme(l.lexeme) + ":*"
	}
	return tsLexeme(l.lexeme)
}

// plan is how a question is looked up.
type plan struct {
	lookups  []lookup
	words    int             // words with at least one lookup: a text holding all of them is a full match
	order    []int           // those words, the first a text keeps longest: the rarest, a word no text holds last
	common   []string        // every word was common: their lexemes in the first index, all required
	weight   float64         // the common words' summed weight, when common is used
	avgLen   float64         // the first index's average text length (search_term '#len')
	terms    []Term          // the question's terms, for their Or conditions
	orWeight map[int]float64 // the weight of a term held by an Or condition
}

func (p plan) empty() bool { return p.words == 0 && len(p.common) == 0 }

// units is how many of the question's words a text must hold to hold them all.
func (p plan) units() int {
	if p.words == 0 {
		return len(p.common)
	}
	return p.words
}

// Term is one word of a question, with the other ways a text holds it.
type Term struct {
	Text string   // looked up in the index, in its forms; "" for none
	Alts []string // other spellings of it, looked up too (a synonym)
	// Or are SQL conditions over the searched table's alias that hold it too
	// ("l.kind = ANY($1)": "قانون" is the kind of a law, which its title
	// seldom says). $1, $2... are Args.
	Or   []string
	Args []any
	// Keep keeps the term in every bucket: the words set aside are the
	// others ("قانون" in "قانون الغابات اللبناني" says what kind of text).
	Keep bool
}

// Terms are the words of a question as terms with no alternatives.
func Terms(q string) []Term {
	var out []Term
	for _, w := range Words(q) {
		out = append(out, Term{Text: w})
	}
	return out
}

// form is one way a word of the question is looked up.
type form struct {
	text   string
	word   int     // the question's word it is a form of
	weight float64 // what a match on it counts against the word as written
	alt    bool    // another spelling of the word (Term.Alts)
}

// forms are the forms each term is looked up in: as written and, for an
// Arabic word, without a leading "و" or "ف" ("and", "so"). The Arabic
// stemmer keeps them before an alef ("والخامسة" stems to "والخامس"), so
// "والخامسة" would find only the texts that write "والخامسة". The word as
// written stays a form: "وكالة" is not "و" and "كالة". A term's other
// spellings count as another form.
func forms(terms []Term) []form {
	var out []form
	for i, t := range terms {
		for j, w := range append([]string{t.Text}, t.Alts...) {
			if w == "" {
				continue
			}
			weight := 1.0
			if j > 0 {
				weight = stemWeight
			}
			out = append(out, form{w, i, weight, j > 0})
			n := arNorm(w)
			for _, p := range []string{"و", "ف"} {
				if strings.HasPrefix(n, p) && utf8.RuneCountInString(n)-1 >= 3 && hasArabic(n) {
					out = append(out, form{n[len(p):], i, stemWeight * weight, j > 0})
					break
				}
			}
		}
	}
	return out
}

// planWords reads each word's lexemes in each index and their rarity from
// search_term, in one query.
func planWords(ctx context.Context, db Querier, terms []Term, idx []index) (plan, error) {
	p := plan{terms: terms}
	if len(terms) == 0 {
		return p, nil
	}
	fs := forms(terms)
	texts := make([]string, len(fs))
	for i, f := range fs {
		texts[i] = f.text
	}
	var parts []string
	for i, x := range idx {
		// a word's texts; for a prefix, the texts of every word it starts
		// (an upper bound: a text with two of them counts twice), from the
		// prefix index on search_term (search_term_prefix)
		docs := fmt.Sprintf(`LEFT JOIN search_term st ON st.corpus = '%s' AND st.term = l`, x.corpus)
		if x.prefix {
			// the range, not LIKE: a pattern made at run time is not an index condition
			docs = fmt.Sprintf(`LEFT JOIN LATERAL (SELECT sum(docs)::int AS docs FROM search_term
				WHERE corpus = '%s' AND term ~>=~ l AND term ~<~ (l || chr(1114111))) st ON true`, x.corpus)
		}
		parts = append(parts, fmt.Sprintf(`SELECT %d AS idx, w.ord::int - 1 AS form, l AS lexeme, coalesce(st.docs, 0) AS docs,
			coalesce((SELECT docs FROM search_term WHERE corpus = '%s' AND term = ''), 0) AS n
			FROM unnest($1::text[]) WITH ORDINALITY w(word, ord)
			CROSS JOIN LATERAL unnest(%s) l
			%s`, i, x.corpus, x.lexemes("w.word"), docs))
	}
	parts = append(parts, fmt.Sprintf(`SELECT -1, -1, '', coalesce((SELECT docs FROM search_term WHERE corpus = '%s' AND term = '#len'), 0), 0`, idx[0].corpus))
	rows, err := db.Query(ctx, strings.Join(parts, " UNION ALL "), texts)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	rowsN := 1.0 // the first index's row count, for the weight of a term held by its Or alone
	type cand struct {
		lookup
		docs    float64
		common  bool
		written bool // the word as written, not another form
		alt     bool // another spelling of the word
	}
	var all []cand
	for rows.Next() {
		var c cand
		var f int
		var n float64
		if err := rows.Scan(&c.index, &f, &c.lexeme, &c.docs, &n); err != nil {
			return p, err
		}
		if c.index < 0 {
			p.avgLen = c.docs
			continue
		}
		if idx[c.index].prefix && utf8.RuneCountInString(c.lexeme) < 4 {
			continue // "حق" starts too many words
		}
		c.word, c.written, c.alt = fs[f].word, fs[f].weight == 1, fs[f].alt
		// BM25's idf
		n = math.Max(n, 1)
		if c.index == 0 {
			rowsN = n
		}
		c.weight = math.Log(1+(n-c.docs+0.5)/(c.docs+0.5)) * idx[c.index].weight * fs[f].weight
		c.common = n > 100 && c.docs > commonShare*n
		all = append(all, c)
	}
	if err := rows.Err(); err != nil {
		return p, err
	}
	// Another spelling, and a condition (a kind, a number), count at most
	// stemWeight of the word as written: "التابعية" is rarer in titles than
	// "الجنسية" and would otherwise outrank the titles that say "الجنسية";
	// a text whose title says "قانون" is closer than one that only is a law.
	written := map[int]float64{}
	for _, c := range all {
		if !c.alt && !c.common {
			written[c.word] = math.Max(written[c.word], c.weight)
		}
	}
	for i := range all {
		if w := written[all[i].word]; all[i].alt && w > 0 {
			all[i].weight = math.Min(all[i].weight, stemWeight*w)
		}
	}
	// Each word's weight is its best lookup's. A word no text holds (one
	// search_term does not know) has the highest weight of all, but every
	// text it is required in is none: it goes last, the first set aside.
	best := map[int]float64{}
	held := map[int]bool{}
	for _, c := range all {
		if c.common && len(terms[c.word].Or) == 0 {
			continue
		}
		if c.weight > best[c.word] {
			best[c.word] = c.weight
		}
		if c.docs > 0 {
			held[c.word] = true
		}
	}
	p.orWeight = map[int]float64{}
	for i, t := range terms {
		if len(t.Or) > 0 {
			held[i] = true // the condition may hold where no lexeme does
			p.orWeight[i] = math.Log(1 + (rowsN-rowsN/1000+0.5)/(rowsN/1000+0.5))
			if w := written[i]; w > 0 {
				p.orWeight[i] = stemWeight * w
			}
			best[i] = math.Max(best[i], p.orWeight[i])
		}
	}
	var kept []int
	for w := range best {
		kept = append(kept, w)
	}
	sort.Slice(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if terms[a].Keep != terms[b].Keep {
			return terms[a].Keep
		}
		if held[a] != held[b] {
			return held[a]
		}
		return best[a] > best[b] || best[a] == best[b] && a < b
	})
	if len(kept) > maxWords {
		kept = kept[:maxWords]
	}
	keep := map[int]bool{}
	for _, w := range kept {
		keep[w] = true
	}
	at := map[string]int{} // a word's lexeme in an index once, at its best weight
	for _, c := range all {
		if (c.common && len(terms[c.word].Or) == 0) || !keep[c.word] {
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

// first is SQL that holds when a text holds the plan's first word, the one
// every candidate holds (the rarest; every word when all are common); "" for
// an empty plan. Its parameters are appended to params.
func (p plan) first(idx []index, params *[]any) string {
	var or []string
	if p.words == 0 {
		var all []string
		for _, l := range p.common {
			*params = append(*params, tsLexeme(l))
			all = append(all, fmt.Sprintf("%s @@ $%d::tsquery", idx[0].vector, len(*params)))
		}
		return strings.Join(all, " AND ")
	}
	w := p.order[0]
	for _, l := range p.lookups {
		if l.word == w {
			*params = append(*params, l.query(idx))
			or = append(or, fmt.Sprintf("%s @@ $%d::tsquery", idx[l.index].vector, len(*params)))
		}
	}
	t := p.terms[w]
	if len(t.Or) > 0 {
		base := len(*params)
		*params = append(*params, t.Args...)
		for _, c := range t.Or {
			or = append(or, renumber(c, base))
		}
	}
	if len(or) == 0 {
		return ""
	}
	return "(" + strings.Join(or, " OR ") + ")"
}

// all is SQL that holds when a text holds every word of the plan; "" for an
// empty plan. Its parameters are appended to params.
func (p plan) all(idx []index, params *[]any) string {
	if p.words == 0 {
		var all []string
		for _, l := range p.common {
			*params = append(*params, tsLexeme(l))
			all = append(all, fmt.Sprintf("%s @@ $%d::tsquery", idx[0].vector, len(*params)))
		}
		return strings.Join(all, " AND ")
	}
	var and []string
	for _, w := range p.order {
		var or []string
		for _, l := range p.lookups {
			if l.word == w {
				*params = append(*params, l.query(idx))
				or = append(or, fmt.Sprintf("%s @@ $%d::tsquery", idx[l.index].vector, len(*params)))
			}
		}
		t := p.terms[w]
		if len(t.Or) > 0 {
			base := len(*params)
			*params = append(*params, t.Args...)
			for _, c := range t.Or {
				or = append(or, renumber(c, base))
			}
		}
		and = append(and, "("+strings.Join(or, " OR ")+")")
	}
	return strings.Join(and, " AND ")
}

// source is what a search reads candidates from.
type source struct {
	from   string // the FROM clause
	keys   string // the key columns, as "expr AS name"
	names  string // their names, comma separated
	length string // the text's length, over from (a stored column where the text is long)
	// perRow scores each text by the lookups it holds (the word as written
	// above its start or its stem), not by the bucket's words: for short
	// texts, where reading each one's vector costs little.
	perRow bool
	// unique: each row of from is one text (names is its key), so a single
	// bucket has no duplicates to sort out
	unique bool
}

// BM25's constants. Each word counts once: the index says whether a text
// holds a word, not how often, so the score is the words' summed rarity
// scaled by the text's length against the average (a match in a short text
// is worth more).
const bm25K1, bm25B = 1.2, 0.75

// candidates is SQL for every candidate of a plan: its keys (names), units
// (how many of the question's words it holds) and score, from the rows of
// k.from that pass filter. Its parameters are appended to params.
func candidates(p plan, idx []index, k source, filter string, params *[]any) string {
	and := ""
	if filter != "" {
		and = " AND " + filter
	}
	avg := math.Max(p.avgLen, 1)
	norm := func(length string) string {
		return fmt.Sprintf("%g / (1 + %g * (%g + %g * (%s)::float8 / %g))", bm25K1+1, bm25K1, 1-bm25B, bm25B, length, avg)
	}
	if p.words == 0 { // every word common: the texts holding all of them, the shortest first
		var qs []string
		for _, l := range p.common {
			*params = append(*params, tsLexeme(l))
			qs = append(qs, fmt.Sprintf("$%d::tsquery", len(*params)))
		}
		return fmt.Sprintf(`SELECT %s, %d AS units, (%g * %s)::float8 AS score FROM %s WHERE %s @@ (%s)%s ORDER BY %s LIMIT 5000`,
			k.keys, len(p.common), p.weight, norm(k.length), k.from, idx[0].vector, strings.Join(qs, " && "), and, k.length)
	}
	// One parameter per lookup, shared by every bucket.
	arg := make([]int, len(p.lookups))
	for i, l := range p.lookups {
		*params = append(*params, l.query(idx))
		arg[i] = len(*params)
	}
	// Each word: any of its lookups, and its weight (the best of them).
	byWord := map[int][]string{}
	best := map[int]float64{}
	held := map[int][]string{} // perRow: each lookup's weight when the text holds it
	for i, l := range p.lookups {
		c := fmt.Sprintf("%s @@ $%d::tsquery", idx[l.index].vector, arg[i])
		byWord[l.word] = append(byWord[l.word], c)
		held[l.word] = append(held[l.word], fmt.Sprintf("CASE WHEN %s THEN %g ELSE 0 END", c, l.weight))
		best[l.word] = math.Max(best[l.word], l.weight)
	}
	for _, w := range p.order {
		t := p.terms[w]
		if len(t.Or) == 0 {
			continue
		}
		base := len(*params)
		*params = append(*params, t.Args...)
		for _, c := range t.Or {
			c = renumber(c, base)
			byWord[w] = append(byWord[w], c)
			held[w] = append(held[w], fmt.Sprintf("CASE WHEN %s THEN %g ELSE 0 END", c, p.orWeight[w]))
		}
		best[w] = math.Max(best[w], p.orWeight[w])
	}
	weightOf := func(words []int) string {
		if !k.perRow {
			sum := 0.0
			for _, w := range words {
				sum += best[w]
			}
			return fmt.Sprintf("%g", sum)
		}
		var terms []string
		for _, w := range words {
			if len(held[w]) == 1 {
				terms = append(terms, held[w][0])
			} else {
				terms = append(terms, "GREATEST("+strings.Join(held[w], ", ")+")")
			}
		}
		return "(" + strings.Join(terms, " + ") + ")"
	}
	// Buckets, Meilisearch's "frequency" strategy in one statement: the texts
	// holding every word, then those holding all but the commonest word, then
	// all but the two commonest, down to the rarest word alone. Each bucket
	// is an intersection of index bitmaps; a text is kept in the best bucket
	// it is in, and scored by the summed rarity of that bucket's words scaled
	// by its length.
	// A bucket is read only while the buckets above it hold fewer than enough
	// texts: Postgres checks that once (a one-time filter) and then skips the
	// scan, so a question that many texts match word for word costs one
	// intersection.
	var ctes, names []string
	for n := len(p.order); n >= 1; n-- {
		var conds []string
		for _, w := range p.order[:n] {
			conds = append(conds, "("+strings.Join(byWord[w], " OR ")+")")
		}
		gate := ""
		if len(names) > 0 {
			var above []string
			for _, nm := range names {
				above = append(above, "SELECT 1 FROM "+nm)
			}
			gate = fmt.Sprintf(" AND (SELECT count(*) FROM (%s LIMIT %d) f) < %d", strings.Join(above, " UNION ALL "), enough, enough)
		}
		name := fmt.Sprintf("bucket%d", n)
		ctes = append(ctes, fmt.Sprintf(`%s AS MATERIALIZED (SELECT %s, %d AS units, (%s * %s)::float8 AS score FROM %s WHERE %s%s%s)`,
			name, k.keys, n, weightOf(p.order[:n]), norm(k.length), k.from, strings.Join(conds, " AND "), and, gate))
		names = append(names, name)
	}
	if len(names) == 1 && k.unique {
		// one bucket of texts each read once: nothing to merge. It stays a
		// MATERIALIZED CTE: inlined, Postgres may walk the table in the
		// ranking's order and test every text's vector (2.5 s for "العمل").
		return fmt.Sprintf(`WITH %s SELECT * FROM %s`, ctes[0], names[0])
	}
	var all []string
	for _, nm := range names {
		all = append(all, "SELECT * FROM "+nm)
	}
	return fmt.Sprintf(`WITH %s SELECT DISTINCT ON (%s) * FROM (%s) b ORDER BY %s, units DESC, score DESC`,
		strings.Join(ctes, ", "), k.names, strings.Join(all, " UNION ALL "), k.names)
}

var argRef = regexp.MustCompile(`\$(\d+)`)

// renumber shifts the $n of a term's condition past base parameters.
func renumber(sql string, base int) string {
	return argRef.ReplaceAllStringFunc(sql, func(m string) string {
		n, _ := strconv.Atoi(m[1:])
		return "$" + strconv.Itoa(base+n)
	})
}

// Words are the words of a question a search looks up.
func Words(q string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(q, unicode.IsSpace) {
		if w = edgePunct.ReplaceAllString(w, ""); w != "" {
			out = append(out, w)
		}
	}
	return out
}

var (
	edgePunct  = regexp.MustCompile(`^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$`)
	arabicChar = regexp.MustCompile(`[\x{0600}-\x{06FF}]`)
)

func hasArabic(s string) bool { return arabicChar.MatchString(s) }

// arNorm is Postgres's ar_norm (db/schema.sql): no tashkeel or tatweel, أ إ آ
// ٱ as ا, ى as ي, ة as ه.
func arNorm(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case (r >= 0x064B && r <= 0x065F) || r == 0x0670 || r == 0x0640:
			continue
		case r == 'أ' || r == 'إ' || r == 'آ' || r == 'ٱ':
			b.WriteRune('ا')
		case r == 'ى':
			b.WriteRune('ي')
		case r == 'ة':
			b.WriteRune('ه')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// tsLexeme is a lexeme as a tsquery literal.
func tsLexeme(t string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(t, `\`, `\\`), "'", "''") + "'"
}
