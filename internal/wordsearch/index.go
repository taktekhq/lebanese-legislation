package wordsearch

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"strconv"
	"sync/atomic"
)

// Index answers the searches of a read-only database from word lists made
// once from it: for each lexeme of a corpus, the texts that hold it, in
// ascending order of their number. A search still plans its words in
// Postgres (planWords: normalizing, stemming, rarity, in one query), then
// collects and ranks the candidates here by page()'s rules, and asks
// Postgres only for the phrase test of the texts it returns. The lexemes are
// the ones Postgres put in the texts' tsvectors, so a list holds exactly the
// texts a GIN lookup finds.
//
// The lists are a file made with the image (BuildIndex), which the server
// maps read-only (LoadIndexFile): 88 MB for 45 million entries, read in
// place; only the texts' columns (12 MB) are copied to the heap. Tested against the same lists in a Postgres table, read
// with one primary-key lookup per word (9 Nov 2026): replies identical, but
// a search took about twice the CPU, and the p95 of 6 searches at once fell
// to 0.49 to 0.55 of the GIN search's, against 0.35 to 0.46 from the file.
//
// The answers are page()'s, byte for byte; where they could differ (texts
// of equal length straddling a LIMIT, a cursor, a term held by a condition)
// the search runs its SQL instead.
type Index struct {
	fams      map[string]*Docs
	lists     ListSource
	fallbacks atomic.Int64
}

// Fallbacks is how many searches the index left to the SQL.
func (x *Index) Fallbacks() int64 { return x.fallbacks.Load() }

// exact passes err on, counting errNotExact.
func (x *Index) exact(err error) error {
	if err == errNotExact {
		x.fallbacks.Add(1)
	}
	return err
}

// Docs are a family's texts, by number. Every family has ID (the law's id
// for an article), Len (the length the score scales by, as candidates()
// reads it), Tie (a law's issue date or a ruling's decision date, in days
// since 0001-01-01; 0 for none) and Code (a major code's). Articles have
// Pos; laws have Year (of issue, 0 for none) and Number ("" for none).
type Docs struct {
	ID, Pos, Len, Tie, Year []int32
	Number                  []string
	Code                    []bool
}

// ListReq asks for a lexeme's texts in a corpus: those holding the lexeme,
// or with Prefix those holding a lexeme that starts with it ('x':*).
type ListReq struct {
	Corpus, Lexeme string
	Prefix         bool
}

// ListSource is where the lists are read (MemLists; a test may use another).
type ListSource interface {
	// Lists are each request's texts, ascending; nil for none.
	Lists(ctx context.Context, reqs []ListReq) ([][]int32, error)
}

// NewIndex is an index over these families' texts and lists. Code is set
// from codes, the major codes' law ids (Known.Codes).
func NewIndex(fams map[string]*Docs, lists ListSource, codes []int32) *Index {
	isCode := map[int32]bool{}
	for _, c := range codes {
		isCode[c] = true
	}
	for name, d := range fams {
		d.Code = make([]bool, len(d.ID))
		if name == "ruling" {
			continue
		}
		for i, id := range d.ID {
			d.Code[i] = isCode[id]
		}
	}
	return &Index{fams: fams, lists: lists}
}

// family is the family of texts a corpus indexes.
func family(corpus string) string {
	switch corpus {
	case "article_exact", "article_stem":
		return "article"
	case "article_fr":
		return "article_fr"
	case "ruling_exact", "ruling_stem":
		return "ruling"
	case "law_title", "law_title_base":
		return "law"
	case "law_title_en":
		return "law_en"
	}
	return ""
}

// errNotExact: the index cannot tell which texts the SQL would return; the
// caller runs the SQL.
var errNotExact = errors.New("wordsearch: the index cannot answer this exactly")

// idxPage is one kind's ranking for the index, as pageSQL is for page().
type idxPage struct {
	fam      string
	filter   func(docs *Docs, d int32) bool // candidates()'s filter; nil for none
	perRow   bool                           // source.perRow
	least    int
	fallback bool
	titles   []int32 // titled: an article whose law is one of these
	unserved []int32 // a text of one of these laws is not served (articles)
	idDesc   bool
	noCount  bool
	noPhrase bool   // the caller does not read Hit.Phrase
	phrase   string // SQL for the phrase test over the joined table; $1 is the question
	join     string // the table joined to u(id, pos) for it
	text     string
}

// The scores, as Postgres computes the SQL candidates() writes:
// (W * 2.2 / (1 + 1.2 * (0.25 + 0.75 * (length)::float8 / avg)))::float8,
// where W is a numeric literal (a sum of them, per row). W * 2.2 is numeric
// (exact) and becomes float8 at the division; the rest is float8, in that
// order, each operation rounded (explicit conversions keep Go from fusing a
// multiply and an add). The constants are read back from the text
// candidates() prints, so they are the same float64s.
type scorer struct {
	k1p1           *big.Rat
	k1, b0, b, avg float64
}

func newScorer(avgLen float64) scorer {
	f := func(v float64) float64 {
		g, _ := strconv.ParseFloat(fmt.Sprintf("%g", v), 64)
		return g
	}
	return scorer{k1p1: decimal(bm25K1 + 1), k1: f(bm25K1), b0: f(1 - bm25B), b: f(bm25B), avg: f(max(avgLen, 1))}
}

// decimal is the numeric value of v as candidates() prints it (%g).
func decimal(v float64) *big.Rat {
	r, _ := new(big.Rat).SetString(fmt.Sprintf("%g", v))
	return r
}

// scaled is W * 2.2 as float8, for a W written as these weights' sum.
func (s scorer) scaled(ws ...float64) float64 {
	sum := new(big.Rat)
	for _, w := range ws {
		sum.Add(sum, decimal(w))
	}
	f, _ := sum.Mul(sum, s.k1p1).Float64()
	return f
}

// score is scaled / the length's denominator.
func (s scorer) score(scaled float64, length int32) float64 {
	t := float64(s.b * float64(length))
	t = float64(t / s.avg)
	t = float64(s.b0 + t)
	t = float64(s.k1 * t)
	t = float64(1 + t)
	return float64(scaled / t)
}

// cand is one candidate and its place in the ranking.
type cand struct {
	doc    int32
	units  int32
	code   bool
	titled bool
	score  float64
	tie    int32
	id     int32
	pos    int32
}

func (q *idxPage) before(a, b *cand) bool {
	switch {
	case a.units != b.units:
		return a.units > b.units
	case a.code != b.code:
		return a.code
	case a.titled != b.titled:
		return a.titled
	case a.score != b.score:
		return a.score > b.score
	case a.tie != b.tie:
		return a.tie > b.tie
	case a.id != b.id:
		return (a.id > b.id) == q.idDesc
	}
	return a.pos < b.pos
}

// commonCut is candidates()'s LIMIT when every word is common.
const commonCut = 5000

// candidates is candidates(p, idx, ...) from the index: each text's units
// and score. edge is the length of texts that straddle the common words'
// cut (Postgres keeps some of them, its sort decides which), -1 for none:
// they are all kept, and an answer returning one of them is not exact.
func (x *Index) candidates(ctx context.Context, p plan, idx []index, q *idxPage, docs *Docs) (cs []cand, edge int32, err error) {
	edge = -1
	sc := newScorer(p.avgLen)
	keep := func(d int32) bool { return q.filter == nil || q.filter(docs, d) }
	if p.words == 0 {
		// every word common: the texts holding all of them, the shortest first
		reqs := make([]ListReq, len(p.common))
		for i, l := range p.common {
			reqs[i] = ListReq{Corpus: idx[0].corpus, Lexeme: l}
		}
		ls, err := x.lists.Lists(ctx, reqs)
		if err != nil {
			return nil, -1, err
		}
		ds := slices.Clone(ls[0])
		for _, l := range ls[1:] {
			ds = intersect(ds, l)
		}
		ds = slices.DeleteFunc(ds, func(d int32) bool { return !keep(d) })
		if len(ds) > commonCut {
			// The shortest commonCut: every text up to the length the
			// commonCut-th has. When more than commonCut are that short, the
			// texts of that length straddle the cut. Found by counting
			// lengths, not sorting ("2" is in 102,605 articles: sorting them
			// took 110 ms before a search the SQL had to answer anyway).
			l := cutLength(docs.Len, ds, commonCut)
			n := 0
			ds = slices.DeleteFunc(ds, func(d int32) bool {
				if docs.Len[d] > l {
					return true
				}
				n++
				return false
			})
			if n > commonCut {
				edge = l
			}
		}
		scaled := sc.scaled(p.weight)
		cs = make([]cand, len(ds))
		for i, d := range ds {
			cs[i] = cand{doc: d, units: int32(len(p.common)), score: sc.score(scaled, docs.Len[d])}
		}
		return cs, edge, nil
	}
	reqs := make([]ListReq, len(p.lookups))
	for i, l := range p.lookups {
		reqs[i] = ListReq{Corpus: idx[l.index].corpus, Lexeme: l.lexeme, Prefix: idx[l.index].prefix}
	}
	ls, err := x.lists.Lists(ctx, reqs)
	if err != nil {
		return nil, -1, err
	}
	// Each word: the texts holding any of its lookups.
	best := map[int]float64{}
	for _, l := range p.lookups {
		best[l.word] = max(best[l.word], l.weight)
	}
	sets := make([][]int32, len(p.order))
	for i, w := range p.order {
		for j, l := range p.lookups {
			if l.word == w {
				sets[i] = union(sets[i], ls[j])
			}
		}
	}
	// A text's units: how many of the words in order it holds before the
	// first it lacks (it is in the buckets up to that many).
	ds := slices.DeleteFunc(slices.Clone(sets[0]), func(d int32) bool { return !keep(d) })
	us := make([]int32, len(ds))
	for i := range us {
		us[i] = 1
	}
	for n := 1; n < len(sets); n++ {
		s, k := sets[n], 0
		for i, d := range ds {
			if us[i] != int32(n) {
				continue
			}
			for k < len(s) && s[k] < d {
				k++
			}
			if k < len(s) && s[k] == d {
				us[i]++
			}
		}
	}
	// The buckets read: every word's, then down while the ones above hold
	// fewer than enough texts.
	count := make([]int, len(sets)+2)
	for _, u := range us {
		count[u]++
	}
	for n := len(sets) - 1; n >= 1; n-- {
		count[n] += count[n+1]
	}
	lowest, above := len(sets), count[len(sets)]
	for n := len(sets) - 1; n >= 1 && above < enough; n-- {
		above += count[n]
		lowest = n
	}
	if !q.perRow {
		// a bucket's weight: its words' best weights summed in Go, printed
		scaled := make([]float64, len(sets)+1)
		for n := 1; n <= len(sets); n++ {
			sum := 0.0
			for _, w := range p.order[:n] {
				sum += best[w]
			}
			scaled[n] = sc.scaled(sum)
		}
		for i, d := range ds {
			if us[i] >= int32(lowest) {
				cs = append(cs, cand{doc: d, units: us[i], score: sc.score(scaled[us[i]], docs.Len[d])})
			}
		}
		return cs, -1, nil
	}
	// perRow: each word counts the best weight among the lookups the text
	// holds (GREATEST of CASEs), the words summed as numeric.
	for i, d := range ds {
		if us[i] < int32(lowest) {
			continue
		}
		ws := make([]float64, 0, us[i])
		for _, w := range p.order[:us[i]] {
			held := 0.0
			for j, l := range p.lookups {
				if l.word == w && l.weight > held {
					if _, ok := slices.BinarySearch(ls[j], d); ok {
						held = l.weight
					}
				}
			}
			ws = append(ws, held)
		}
		cs = append(cs, cand{doc: d, units: us[i], score: sc.score(sc.scaled(ws...), docs.Len[d])})
	}
	return cs, -1, nil
}

// cutLength is the smallest length that n of the texts ds are no longer
// than (n <= len(ds)).
func cutLength(lens []int32, ds []int32, n int) int32 {
	hi := int32(0)
	for _, d := range ds {
		hi = max(hi, lens[d])
	}
	if hi > 1<<22 { // a count per length would be large: sort the lengths
		ls := make([]int32, len(ds))
		for i, d := range ds {
			ls[i] = lens[d]
		}
		slices.Sort(ls)
		return ls[n-1]
	}
	count := make([]int32, hi+1)
	for _, d := range ds {
		count[max(lens[d], 0)]++
	}
	seen := 0
	for l, c := range count {
		if seen += int(c); seen >= n {
			return int32(l)
		}
	}
	return hi
}

// page is page(candidates(p, idx, ...)) from the index: the same hits in the
// same order, the same counts.
func (x *Index) page(ctx context.Context, db Querier, p plan, idx []index, q idxPage, limit int) (Page, error) {
	if limit <= 0 {
		limit = 10
	}
	docs := x.fams[q.fam]
	if docs == nil || !x.covers(p, idx, q.fam) {
		return Page{}, errNotExact
	}
	cs, edge, err := x.candidates(ctx, p, idx, &q, docs)
	if err != nil {
		return Page{}, err
	}
	if edge >= 0 && !q.noCount {
		return Page{}, errNotExact // the count would depend on the texts kept
	}
	return q.rank(ctx, db, docs, cs, edge, p.units(), limit)
}

// covers says the index has every list a plan reads: its corpora are this
// family's, and no term is held by a condition.
func (x *Index) covers(p plan, idx []index, fam string) bool {
	for _, i := range idx {
		if family(i.corpus) != fam {
			return false
		}
	}
	for _, t := range p.terms {
		if len(t.Or) > 0 {
			return false
		}
	}
	return true
}

// rank is page()'s r and its first limit+1 rows: the candidates holding at
// least least words, served, in the ranking's order, with the phrase test.
func (q *idxPage) rank(ctx context.Context, db Querier, docs *Docs, cs []cand, edge int32, units, limit int) (Page, error) {
	least := q.least
	if q.fallback && units > q.least {
		for _, c := range cs {
			if int(c.units) >= units {
				least = units // every word when any text holds them all
				break
			}
		}
	}
	cs = slices.DeleteFunc(cs, func(c cand) bool {
		return int(c.units) < least || (len(q.unserved) > 0 && slices.Contains(q.unserved, docs.ID[c.doc]))
	})
	for i := range cs {
		c := &cs[i]
		c.id, c.tie, c.code = docs.ID[c.doc], docs.Tie[c.doc], docs.Code[c.doc]
		if docs.Pos != nil {
			c.pos = docs.Pos[c.doc]
		}
		c.titled = len(q.titles) > 0 && slices.Contains(q.titles, c.id)
	}
	top := q.top(cs, limit+1)
	for _, c := range top {
		if docs.Len[c.doc] == edge {
			return Page{}, errNotExact
		}
	}
	var out Page
	if len(top) == 0 {
		return out, nil
	}
	if !q.noCount {
		out.Total = len(cs)
		for _, c := range cs {
			if units > 0 && int(c.units) >= units {
				out.Full++
			}
		}
	}
	phrase := make([]bool, len(top))
	if !q.noPhrase {
		var err error
		if phrase, err = q.phrases(ctx, db, top); err != nil {
			return Page{}, err
		}
	}
	for i, c := range top {
		out.Hits = append(out.Hits, Hit{ID: int(c.id), Position: int(c.pos), Code: c.code, Phrase: phrase[i],
			Full: units > 0 && int(c.units) >= units,
			Key:  Key{Units: int(c.units), Code: c.code, Titled: c.titled, Score: c.score, Tie: int(c.tie), ID: int(c.id), Position: int(c.pos)}})
	}
	if len(out.Hits) > limit {
		out.Hits, out.More = out.Hits[:limit], true
	}
	return out, nil
}

// top are the first n candidates in the ranking's order.
func (q *idxPage) top(cs []cand, n int) []cand {
	out := make([]cand, 0, n)
	for i := range cs {
		c := &cs[i]
		if len(out) == n && !q.before(c, &out[n-1]) {
			continue
		}
		at := sort.Search(len(out), func(j int) bool { return q.before(c, &out[j]) })
		if len(out) < n {
			out = append(out, cand{})
		}
		copy(out[at+1:], out[at:len(out)-1])
		out[at] = *c
	}
	return out
}

// phrases tests the question as a phrase in each hit, as page() does for the
// rows it returns.
func (q *idxPage) phrases(ctx context.Context, db Querier, top []cand) ([]bool, error) {
	ids := make([]int32, len(top))
	pos := make([]int32, len(top))
	for i, c := range top {
		ids[i], pos[i] = c.id, c.pos
	}
	rows, err := db.Query(ctx, `SELECT u.i, (`+q.phrase+`) FROM unnest($2::int[], $3::int[]) WITH ORDINALITY u(id, pos, i) JOIN `+q.join, q.text, ids, pos)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]bool, len(top))
	seen := 0
	for rows.Next() {
		var i int64
		var ph *bool
		if err := rows.Scan(&i, &ph); err != nil {
			return nil, err
		}
		out[i-1] = ph != nil && *ph
		seen++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if seen != len(top) {
		return nil, fmt.Errorf("wordsearch: the index names %d texts the database does not have", len(top)-seen)
	}
	return out, nil
}

// titleLaws is TitleLaws from the index: the texts whose title holds every
// word, the 20 best by score. Texts of equal score straddling the 20th are
// a choice Postgres's sort makes: not exact.
func (x *Index) titleLaws(ctx context.Context, p plan) ([]int32, error) {
	docs := x.fams["law"]
	if docs == nil || !x.covers(p, titleIndexes, "law") {
		return nil, errNotExact
	}
	q := idxPage{fam: "law"}
	cs, _, err := x.candidates(ctx, p, titleIndexes, &q, docs)
	if err != nil {
		return nil, err
	}
	cs = slices.DeleteFunc(cs, func(c cand) bool { return int(c.units) != p.words })
	// the 21 best scores, best first (the 21st only to see a tie at the 20th)
	var best []cand
	for _, c := range cs {
		if len(best) == 21 && c.score <= best[20].score {
			continue
		}
		at := sort.Search(len(best), func(j int) bool { return c.score > best[j].score })
		if len(best) < 21 {
			best = append(best, cand{})
		}
		copy(best[at+1:], best[at:len(best)-1])
		best[at] = c
	}
	cs = best
	if len(cs) > 20 {
		if cs[19].score == cs[20].score {
			return nil, errNotExact
		}
		cs = cs[:20]
	}
	var ids []int32
	for _, c := range cs {
		ids = append(ids, docs.ID[c.doc])
	}
	return ids, nil
}

// laws is Laws from the index (Known set, no cursor, no Except).
func (x *Index) laws(ctx context.Context, db Querier, q LawQuery, p plan, idx []index) (Page, error) {
	fam := "law"
	ip := idxPage{fam: fam, perRow: true, least: q.Least, idDesc: true, noCount: q.NoCount, noPhrase: q.NoPhrase,
		phrase: idx[0].phrase("$1"), join: "legislation l ON l.id = u.id", text: q.Text}
	if q.English {
		fam, ip.fam = "law_en", "law_en"
		ip.join = "legislation l ON l.id = u.id JOIN legislation_title_en te ON te.law_id = u.id"
	}
	docs := x.fams[fam]
	if docs == nil || !x.covers(p, idx, fam) || q.Year > 9999 {
		return Page{}, errNotExact
	}
	if q.Year > 0 || q.Number != "" {
		ip.filter = func(docs *Docs, d int32) bool {
			return (q.Year <= 0 || docs.Year[d] == int32(q.Year)) && (q.Number == "" || docs.Number[d] == q.Number)
		}
	}
	if q.Full || fullOnly(q.Text, !q.English) {
		ip.least = p.units()
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}
	if !p.empty() {
		cs, edge, err := x.candidates(ctx, p, idx, &ip, docs)
		if err != nil {
			return Page{}, err
		}
		if edge >= 0 && !ip.noCount {
			return Page{}, errNotExact
		}
		return ip.rank(ctx, db, docs, cs, edge, p.units(), limit)
	}
	if q.Number == "" {
		return Page{}, nil
	}
	// a number alone ("law 44 of 2015"): every text with it, units 0, score 0
	var cs []cand
	for d := range docs.ID {
		if ip.filter(docs, int32(d)) {
			cs = append(cs, cand{doc: int32(d)})
		}
	}
	return ip.rank(ctx, db, docs, cs, -1, p.units(), limit)
}

// union is a ∪ b, both ascending.
func union(a, b []int32) []int32 {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	out := make([]int32, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		case a[i] > b[j]:
			out = append(out, b[j])
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	return append(out, b[j:]...)
}

// intersect is a ∩ b, both ascending, in a's storage.
func intersect(a, b []int32) []int32 {
	out := a[:0]
	j := 0
	for _, v := range a {
		for j < len(b) && b[j] < v {
			j++
		}
		if j < len(b) && b[j] == v {
			out = append(out, v)
		}
	}
	return out
}
