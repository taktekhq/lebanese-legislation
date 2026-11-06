package mcpserver

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Full-text search. The search code builds FTS5 MATCH expressions exactly as
// the Worker did against its SQLite snapshot (fts5MatchAll, fts5Forms,
// fts5French, fts5English) and this file reads them back into Postgres
// tsqueries against the corpus's own vectors:
//
//   - articles: legislation_article.search_exact, to_tsvector('simple', ar_norm(text_ar))
//   - rulings:  ruling.search_exact, the same over title and text
//   - law titles: to_tsvector('simple', ar_norm(title)), narrowed first by
//     the trigram index on ar_norm(title)
//   - English titles and French article texts: 'simple' vectors over the
//     lower-cased, accent-folded text, as FTS5's remove_diacritics did
//
// FTS5 ranked by bm25; so does this, from the same statistics: the number
// of rows, the average row length, how many rows hold each phrase, and each
// candidate's term frequencies and length, read from its tsvector.

// ftsQuery is a parsed MATCH expression: every group must match, a group
// matches when any of its phrases does.
type ftsQuery struct {
	groups [][]ftsPhrase
}

// ftsPhrase is a quoted FTS5 phrase: its tokens in order, the last one a
// prefix when the phrase was followed by "*".
type ftsPhrase struct {
	tokens []string
	prefix bool
}

// parseFTS reads the expressions the fts5* builders write: phrases
// ("..." or "..."*) and parenthesized OR lists, joined by AND.
func parseFTS(expr string) ftsQuery {
	var q ftsQuery
	rs := []rune(expr)
	i := 0
	readPhrase := func() (ftsPhrase, bool) {
		// at a double quote
		i++
		var b strings.Builder
		for i < len(rs) {
			if rs[i] == '"' {
				if i+1 < len(rs) && rs[i+1] == '"' {
					b.WriteRune('"')
					i += 2
					continue
				}
				i++
				break
			}
			b.WriteRune(rs[i])
			i++
		}
		p := ftsPhrase{tokens: ftsTokens(b.String())}
		if i < len(rs) && rs[i] == '*' {
			p.prefix = true
			i++
		}
		return p, true
	}
	for i < len(rs) {
		switch {
		case rs[i] == '"':
			p, _ := readPhrase()
			q.groups = append(q.groups, []ftsPhrase{p})
		case rs[i] == '(':
			i++
			var alts []ftsPhrase
			for i < len(rs) && rs[i] != ')' {
				if rs[i] == '"' {
					p, _ := readPhrase()
					alts = append(alts, p)
					continue
				}
				i++
			}
			i++
			q.groups = append(q.groups, alts)
		default:
			i++ // spaces, AND, OR
		}
	}
	return q
}

// ftsTokens splits a phrase as FTS5's unicode61 tokenizer does: letters,
// digits and private-use characters make tokens, anything else separates
// them; Latin letters are lower-cased with their accents removed.
func ftsTokens(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		if r < 0x0370 || (r >= 0x1E00 && r <= 0x1EFF) {
			// a Latin letter loses its accent ("é" is "e"); Arabic is
			// left whole (NFD would split ئ and ؤ)
			for _, d := range norm.NFD.String(string(r)) {
				if d >= 0x0300 && d <= 0x036f {
					continue
				}
				r = d
				break
			}
		}
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Co, r):
			b.WriteRune(unicode.ToLower(r))
		default:
			flush()
		}
	}
	flush()
	return out
}

// usable drops phrases with no tokens; ok is false when some group is
// left with none, so the query can match nothing.
func (q ftsQuery) usable() (ftsQuery, bool) {
	var out ftsQuery
	for _, g := range q.groups {
		var alts []ftsPhrase
		for _, p := range g {
			if len(p.tokens) > 0 {
				alts = append(alts, p)
			}
		}
		if len(alts) == 0 {
			return ftsQuery{}, false
		}
		out.groups = append(out.groups, alts)
	}
	return out, len(out.groups) > 0
}

// phrases lists every phrase of the query, in order: bm25 sums over them.
func (q ftsQuery) phrases() []ftsPhrase {
	var out []ftsPhrase
	for _, g := range q.groups {
		out = append(out, g...)
	}
	return out
}

func tsLexeme(t string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(t, `\`, `\\`), "'", "''") + "'"
}

func (p ftsPhrase) tsquery() string {
	parts := make([]string, len(p.tokens))
	for i, t := range p.tokens {
		parts[i] = tsLexeme(t)
		if p.prefix && i == len(p.tokens)-1 {
			parts[i] += ":*"
		}
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " <-> ") + ")"
}

// tsquery is the query as Postgres tsquery text, to be cast with ::tsquery.
func (q ftsQuery) tsquery() string {
	groups := make([]string, len(q.groups))
	for i, g := range q.groups {
		alts := make([]string, len(g))
		for j, p := range g {
			alts[j] = p.tsquery()
		}
		if len(alts) == 1 {
			groups[i] = alts[0]
		} else {
			groups[i] = "(" + strings.Join(alts, " | ") + ")"
		}
	}
	return strings.Join(groups, " & ")
}

// Postgres's parser keeps digits joined by "/", "." or "-" as one token
// ("670/672", "15.4.2014"), where FTS5 splits them, so a phrase with digits
// is matched against the text itself: the words around it go to the
// tsvector, the phrase to a regular expression with FTS5's token
// boundaries.

func (p ftsPhrase) hasDigit() bool {
	for _, t := range p.tokens {
		for _, r := range t {
			if unicode.IsDigit(r) {
				return true
			}
		}
	}
	return false
}

func digitGroup(g []ftsPhrase) bool {
	for _, p := range g {
		if p.hasDigit() {
			return true
		}
	}
	return false
}

// pattern is the phrase as a regular expression over the text.
func (p ftsPhrase) pattern() string {
	parts := make([]string, len(p.tokens))
	for i, t := range p.tokens {
		parts[i] = regexp.QuoteMeta(t)
	}
	re := `(^|[^[:alnum:]])` + strings.Join(parts, `[^[:alnum:]]+`)
	if !p.prefix {
		re += `([^[:alnum:]]|$)`
	}
	return re
}

func (p ftsPhrase) longest() string {
	best := ""
	for _, t := range p.tokens {
		if runeLen(t) > runeLen(best) {
			best = t
		}
	}
	return best
}

// textMatch is the SQL condition that text holds phrase p.
func (p ftsPhrase) textMatch(text string, args *[]any) string {
	*args = append(*args, "%"+likeEscape(p.longest())+"%", p.pattern())
	return fmt.Sprintf("(%s ILIKE $%d AND %s ~* $%d)", text, len(*args)-1, text, len(*args))
}

// where is the SQL condition for the query over a row whose search vector
// is vec and whose text is text.
func (q ftsQuery) where(vec, text string, args *[]any) string {
	var lexical, digits ftsQuery
	for _, g := range q.groups {
		if digitGroup(g) {
			digits.groups = append(digits.groups, g)
		} else {
			lexical.groups = append(lexical.groups, g)
		}
	}
	var conds []string
	if len(lexical.groups) > 0 {
		*args = append(*args, lexical.tsquery())
		conds = append(conds, fmt.Sprintf("%s @@ $%d::tsquery", vec, len(*args)))
	} else {
		// Only numbers: the index still narrows the rows, by each phrase's
		// first token as a prefix ("670" finds "670/672").
		var loose ftsQuery
		for _, g := range digits.groups {
			var alts []ftsPhrase
			for _, p := range g {
				alts = append(alts, ftsPhrase{tokens: p.tokens[:1], prefix: true})
			}
			loose.groups = append(loose.groups, alts)
		}
		*args = append(*args, loose.tsquery())
		conds = append(conds, fmt.Sprintf("%s @@ $%d::tsquery", vec, len(*args)))
	}
	for _, g := range digits.groups {
		alts := make([]string, len(g))
		for i, p := range g {
			if p.hasDigit() {
				alts[i] = p.textMatch(text, args)
			} else {
				*args = append(*args, ftsQuery{groups: [][]ftsPhrase{{p}}}.tsquery())
				alts[i] = fmt.Sprintf("%s @@ $%d::tsquery", vec, len(*args))
			}
		}
		conds = append(conds, "("+strings.Join(alts, " OR ")+")")
	}
	return strings.Join(conds, " AND ")
}

// likeEscape escapes s for a LIKE pattern.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// prefilter narrows law titles with conditions the trigram index on
// ar_norm(title) serves, before the tsvector check: each group's longest
// token must occur in the title. Groups with a token too short for
// trigrams are left to the tsvector check alone.
func (q ftsQuery) prefilter(expr, like string, args *[]any) string {
	var conds []string
	for _, g := range q.groups {
		var alts []string
		ok := true
		for _, p := range g {
			best := p.longest()
			if runeLen(best) < 3 {
				ok = false
				break
			}
			*args = append(*args, "%"+likeEscape(best)+"%")
			alts = append(alts, fmt.Sprintf("%s %s $%d", expr, like, len(*args)))
		}
		if ok && len(alts) > 0 {
			conds = append(conds, "("+strings.Join(alts, " OR ")+")")
		}
	}
	return strings.Join(conds, " AND ")
}

// matches reports whether a token list (one row) satisfies the query, the
// way FTS5 would: for the calculated amounts the snapshot indexed beside an
// article's text, which no Postgres vector holds.
func (q ftsQuery) matches(doc []string) bool {
	for _, g := range q.groups {
		hit := false
		for _, p := range g {
			if p.tf(doc) > 0 {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// tf counts the phrase's occurrences in doc.
func (p ftsPhrase) tf(doc []string) int {
	n := 0
	for i := 0; i+len(p.tokens) <= len(doc); i++ {
		ok := true
		for j, t := range p.tokens {
			d := doc[i+j]
			if p.prefix && j == len(p.tokens)-1 {
				if !strings.HasPrefix(d, t) {
					ok = false
				}
			} else if d != t {
				ok = false
			}
			if !ok {
				break
			}
		}
		if ok {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- bm25

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// corpusStats are a searchable table's figures for bm25.
type corpusStats struct {
	rows   float64
	avgLen float64
}

// bm25 is FTS5's bm25(): idf per phrase from how many rows hold it, term
// frequency saturated by k1 and normalized by the row's length. Higher is
// better (FTS5's rank is its negative).
func bm25(st corpusStats, df []float64, tf []float64, dl float64) float64 {
	score := 0.0
	avg := st.avgLen
	if avg <= 0 {
		avg = 1
	}
	for i := range tf {
		idf := math.Log((st.rows - df[i] + 0.5) / (df[i] + 0.5))
		if idf <= 0 {
			idf = 1e-6
		}
		score += idf * (tf[i] * (bm25K1 + 1)) / (tf[i] + bm25K1*(1-bm25B+bm25B*dl/avg))
	}
	return score
}

// tfColumns is the select list that reads, from a tsvector, a row's length
// (dl) and each phrase's term frequency (tf0, tf1...): the occurrences of the
// phrase's last token, or of any token it begins when it is a prefix; a
// phrase with digits is counted in the text instead.
func tfColumns(phrases []ftsPhrase, vec, text string, args *[]any) string {
	cols := []string{"coalesce(sum(cardinality(u.positions)), 0)::float8"}
	for _, p := range phrases {
		if p.hasDigit() {
			*args = append(*args, p.pattern())
			cols = append(cols, fmt.Sprintf("regexp_count(%s, $%d, 1, 'i')::float8", text, len(*args)))
			continue
		}
		last := p.tokens[len(p.tokens)-1]
		*args = append(*args, last)
		cond := fmt.Sprintf("u.lexeme = $%d", len(*args))
		if p.prefix {
			cond = fmt.Sprintf("starts_with(u.lexeme, $%d)", len(*args))
		}
		cols = append(cols, fmt.Sprintf("coalesce(sum(cardinality(u.positions)) FILTER (WHERE %s), 0)::float8", cond))
	}
	return "(SELECT array[" + strings.Join(cols, ", ") + "] FROM unnest(" + vec + ") u)"
}

// statsCache keeps corpus figures and per-phrase row counts for a while:
// the corpus changes once a night.
type statsCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	corpora map[string]*fresh[corpusStats]
	df      map[string]cachedDF
}

type cachedDF struct {
	n  float64
	at time.Time
}

func newStatsCache(ttl time.Duration) *statsCache {
	return &statsCache{ttl: ttl, corpora: map[string]*fresh[corpusStats]{}, df: map[string]cachedDF{}}
}

// corpus is a corpus's statistics (rows, average length), read once and
// reloaded in the background (fresh.go).
func (c *statsCache) corpus(ctx context.Context, key string, load func(context.Context) (corpusStats, error)) (corpusStats, error) {
	c.mu.Lock()
	f, ok := c.corpora[key]
	if !ok {
		f = &fresh[corpusStats]{}
		c.corpora[key] = f
	}
	c.mu.Unlock()
	return f.get(ctx, load)
}

func (c *statsCache) rowsWith(ctx context.Context, key string, load func(context.Context) (float64, error)) (float64, error) {
	c.mu.Lock()
	if v, ok := c.df[key]; ok && time.Since(v.at) < c.ttl {
		c.mu.Unlock()
		return v.n, nil
	}
	if len(c.df) > 50000 {
		c.df = map[string]cachedDF{}
	}
	c.mu.Unlock()
	n, err := load(ctx)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	c.df[key] = cachedDF{n, time.Now()}
	c.mu.Unlock()
	return n, nil
}
