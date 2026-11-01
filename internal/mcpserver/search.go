package mcpserver

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------- search_legislation

// yearVal is the year argument as the Worker read it: tested for
// truthiness and > 0 as a number, written into patterns as typed.
type yearVal struct {
	truthy bool
	f      float64
	s      string
}

func yearFrom(v any) yearVal {
	if v == nil {
		return yearVal{}
	}
	return yearVal{truthy: truthy(v), f: jsToNumber(v), s: jsString(v)}
}

func yearInt(y int) yearVal {
	if y == 0 {
		return yearVal{}
	}
	return yearVal{truthy: true, f: float64(y), s: strconv.Itoa(y)}
}

// positive is the Worker's `year && year > 0`.
func (y yearVal) positive() bool { return y.truthy && y.f > 0 }

// found is one pass of the four searches, in the Worker's field order.
type found struct {
	o     obj
	total int
}

// searchOnce runs one pass of the four searches. num narrows laws by their
// own number; with lawsOnly (a number or a year in a rescued question names
// a text, not an article or a ruling) only the laws are searched. forms
// matches each word in its usual forms (fts5Forms) instead of as typed: a
// late fallback, as short stems ("بيع", "عقد") match more than the
// question meant.
func (s *Server) searchOnce(ctx context.Context, q, kind string, year yearVal, limit int, num string, lawsOnly, forms bool) (found, error) {
	var f found
	want := func(k string) bool { return kind == "" || kind == k }
	if want("laws") {
		rows, err := s.findLaws(ctx, q, year, limit, num, forms)
		if err != nil {
			return f, err
		}
		f.o.set("laws", rows)
		f.o.set("law_count", len(rows))
		f.total += len(rows)
	}
	if want("articles") && !lawsOnly {
		rows, notInCode, err := s.findArticles(ctx, q, limit, forms)
		if err != nil {
			return f, err
		}
		f.o.set("articles", rows)
		f.o.set("article_count", len(rows))
		if notInCode != "" && len(rows) > 0 {
			f.o.set("articles_not_in_code", notInCode)
		}
		f.total += len(rows)
	}
	if want("rulings") && !lawsOnly {
		rows, err := s.findRulings(ctx, q, limit, forms)
		if err != nil {
			return f, err
		}
		f.o.set("rulings", rows)
		f.o.set("ruling_count", len(rows))
		f.total += len(rows)
	}
	if want("gazette") && !lawsOnly {
		rows, count, err := s.findGazette(ctx, q, limit, year)
		if err != nil {
			return f, err
		}
		f.o.set("gazette", rows)
		f.o.set("gazette_count", count)
		f.total += count
	}
	return f, nil
}

var searchKinds = setOf("laws", "articles", "rulings", "gazette")

func (s *Server) searchLegislation(ctx context.Context, a args) (toolResult, error) {
	raw, err := strArg(a["query"])
	if err != nil {
		return toolResult{}, err
	}
	// Arabic-Indic digits ("٢٤٨") are folded to 0-9: the indexed texts use 0-9.
	q := foldDigits(jsTrim(raw))
	if q == "" || runeLen(q) >= 200 {
		return ok(obj{{"q", q}, {"laws", []any{}}, {"law_count", 0}, {"articles", []any{}}, {"article_count", 0},
			{"rulings", []any{}}, {"ruling_count", 0}, {"gazette", []any{}}, {"gazette_count", 0}}), nil
	}
	limit := clampInt(a["limit"], 10, 1, 20)
	kind := ""
	if v, has := a["kind"]; has {
		k, isStr := v.(string)
		if !isStr || !searchKinds[k] {
			return fail(`kind must be one of "laws", "articles", "rulings", "gazette", or omitted`), nil
		}
		kind = k
	}
	year := yearFrom(a["year"])

	out := obj{{"q", q}}
	// The fallbacks below stop starting new searches past this point, so the
	// call stays inside its budget.
	deadline := time.Now().Add(2500 * time.Millisecond)
	f, err := s.searchOnce(ctx, q, kind, year, limit, "", false, false)
	if err != nil {
		return toolResult{}, err
	}
	if f.total == 0 && kind != "gazette" {
		// Nothing matched: read the question again, as the website does. A
		// year given as a filter stays one.
		for _, alt := range rescueReadings(q) {
			named := alt.num != "" || alt.year != 0
			y := year
			if alt.year != 0 {
				y = yearInt(alt.year)
			}
			r, err := s.searchOnce(ctx, alt.query, kind, y, limit, alt.num, named && (kind == "" || kind == "laws"), false)
			if err != nil {
				return toolResult{}, err
			}
			if r.total > 0 {
				f = r
				parts := []string{}
				for _, p := range []string{alt.query, alt.num, yearStr(alt.year)} {
					if p != "" {
						parts = append(parts, p)
					}
				}
				out.set("searched_instead", strings.Join(parts, " "))
				out.set("searched_instead_kind", alt.kind)
				break
			}
		}
	}
	if f.total == 0 && kind != "gazette" && hasArabic(q) {
		// Every word in its usual forms: "تبلغه الموعدين تغيبه", "مرور الزمن الصورية".
		r, err := s.searchOnce(ctx, q, kind, year, limit, "", false, true)
		if err != nil {
			return toolResult{}, err
		}
		if r.total > 0 {
			f = r
			out.set("searched_instead", q+" (each word in any of its forms)")
			out.set("searched_instead_kind", "adjusted")
		}
	}
	if f.total == 0 && kind != "gazette" && hasArabic(q) && time.Now().Before(deadline) {
		// Still nothing: no text holds every word. Leave out one word, trying
		// the one that tells least first (dropOrder), and keep the first
		// reading that finds anything; then the same with the commonest word
		// gone, while two words are left.
		var words []string
		for _, w := range jsFields(q) {
			if !arStop[arNorm(w)] {
				words = append(words, w)
			}
		}
	rounds:
		for round := 0; round < 2 && len(words) > 1; round++ {
			order, err := s.dropOrder(ctx, words, kind)
			if err != nil {
				return toolResult{}, err
			}
			for _, i := range order {
				if time.Now().After(deadline) {
					break rounds
				}
				left := without(words, i)
				// one kind word left alone ("قانون") answers nothing
				if len(left) == 1 && kindWords[arNorm(left[0])] {
					continue
				}
				rest := strings.Join(left, " ")
				r, err := s.searchOnce(ctx, rest, kind, year, limit, "", false, true)
				if err != nil {
					return toolResult{}, err
				}
				if r.total > 0 {
					f = r
					out.set("searched_instead", rest)
					out.set("searched_instead_kind", "adjusted")
					break rounds
				}
			}
			words = without(words, order[0])
		}
	}
	out.merge(f.o)
	return ok(out), nil
}

func yearStr(y int) string {
	if y == 0 {
		return ""
	}
	return strconv.Itoa(y)
}

func without(words []string, i int) []string {
	out := make([]string, 0, len(words)-1)
	for j, w := range words {
		if j != i {
			out = append(out, w)
		}
	}
	return out
}

var kindWords = setOf("قانون", "القانون", "مرسوم", "المرسوم", "قرار", "القرار", "نظام", "النظام", "قوانين", "القوانين")

// dropOrder ranks the words by how little they tell, as the website's
// dropOne does: a word found in no text of the kind searched (articles when
// no kind is given) first, then the one found in the most, counted in its
// usual forms.
func (s *Server) dropOrder(ctx context.Context, words []string, kind string) ([]int, error) {
	counts := make([]float64, len(words))
	for i, w := range words {
		counts[i] = math.Inf(1)
		m := fts5Forms(w)
		if m == "" {
			continue
		}
		var n int
		var err error
		switch kind {
		case "laws":
			n, err = s.countLawTitles(ctx, m, 20000)
		case "rulings":
			n, err = s.countRulings(ctx, m, 20000)
		default:
			n, err = s.countArticles(ctx, m, 20000)
		}
		if err != nil {
			return nil, err
		}
		if n > 0 {
			counts[i] = float64(n)
		}
	}
	order := make([]int, len(words))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(x, y int) bool {
		a, b := order[x], order[y]
		if counts[a] == counts[b] {
			return a < b
		}
		return counts[a] > counts[b]
	})
	return order, nil
}

// latinName folds a Latin-script query for matching code names: lower case,
// accents dropped ("Code pénal" matches "code penal").
func latinName(s string) string {
	return jsTrim(anSpaces.ReplaceAllString(strings.ToLower(stripMarks(s)), " "))
}

// rankCodes keeps the codes whose names hold every word of a Latin-script
// query, closest first: a name equal to the query, then one name holding
// every word (the shorter, the closer), then words spread over the English
// name, the French name and the slug together.
func rankCodes(q string, codes []lawRow) []lawRow {
	query := latinName(q)
	var words []string
	for _, w := range strings.Split(query, " ") {
		words = append(words, strings.TrimSuffix(w, "s"))
	}
	holds := func(name string) bool {
		for _, w := range words {
			if !strings.Contains(name, w) {
				return false
			}
		}
		return true
	}
	type ranked struct {
		code  lawRow
		score float64
	}
	var rs []ranked
	for _, c := range codes {
		var names []string
		for _, n := range []*string{c.CodeNameEn, c.CodeNameFr} {
			if n != nil && *n != "" {
				names = append(names, latinName(*n))
			}
		}
		if c.CodeSlug != nil && *c.CodeSlug != "" {
			names = append(names, latinName(strings.ReplaceAll(*c.CodeSlug, "-", " ")))
		}
		score := math.Inf(1)
		for _, n := range names {
			if n == query {
				score = 0
			} else if holds(n) {
				score = math.Min(score, float64(len(strings.Split(n, " "))))
			}
		}
		if math.IsInf(score, 1) && holds(strings.Join(names, " ")) {
			score = 1000
		}
		if !math.IsInf(score, 1) {
			rs = append(rs, ranked{c, score})
		}
	}
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].score < rs[j].score })
	out := make([]lawRow, len(rs))
	for i, r := range rs {
		out[i] = r.code
	}
	return out
}

var lawIDRe = regexp.MustCompile(`^\d{1,10}$`)

func (s *Server) findLaws(ctx context.Context, q string, year yearVal, limit int, num string, forms bool) ([]any, error) {
	var foundRows []lawRow
	// A question that names one text ("cybercrime" is Law 81 of 2018) has it first (pins in gloss.json).
	if num == "" {
		if pin := pinnedLaw(q); pin != 0 {
			l, err := s.loadLaw(ctx, int64(pin))
			if err != nil {
				return nil, err
			}
			if l != nil {
				foundRows = append(foundRows, *l)
			}
		}
	}
	// A law's id, as get_law and the site's addresses use it.
	if num == "" && lawIDRe.MatchString(q) {
		id, _ := strconv.ParseInt(q, 10, 64)
		l, err := s.loadLaw(ctx, id)
		if err != nil {
			return nil, err
		}
		if l != nil {
			foundRows = append(foundRows, *l)
		}
	}
	// The codes by their English or French name ("Penal Code", "obligations"),
	// closest name first.
	if num == "" && hasLatin(q) {
		rows, err := s.db.Query(ctx, `SELECT `+lawCols+` FROM `+lawFrom+` WHERE c.slug IS NOT NULL ORDER BY l.id`)
		if err != nil {
			return nil, err
		}
		codes, err := scanLaws(rows, nil)
		if err != nil {
			return nil, err
		}
		foundRows = append(foundRows, rankCodes(q, codes)...)
	}
	// The year given as a filter is not a title word too.
	less := q
	if year.positive() {
		var ws []string
		for _, w := range jsFields(q) {
			if w != year.s {
				ws = append(ws, w)
			}
		}
		less = strings.Join(ws, " ")
	}
	titleQ := less
	if jsTrim(less) == "" {
		titleQ = q
	}
	match := fts5MatchAll(titleQ)
	if forms {
		match = fts5Forms(titleQ)
	}
	if match != "" || num != "" {
		rows, err := s.lawTitleSearch(ctx, match, year, num, limit)
		if err != nil {
			return nil, err
		}
		foundRows = append(foundRows, rows...)
	}
	// The unofficial English titles, after every Arabic match (Arabic is official).
	if hasLatin(q) && num == "" {
		if en := fts5English(q); en != "" {
			rows, err := s.englishTitleSearch(ctx, en, year, limit)
			if err != nil {
				return nil, err
			}
			foundRows = append(foundRows, rows...)
		}
	}
	seen := map[int]bool{}
	out := []any{}
	for i := range foundRows {
		l := &foundRows[i]
		if seen[l.ID] {
			continue
		}
		seen[l.ID] = true
		if year.positive() && (l.IssuedOn == nil || !strings.HasPrefix(*l.IssuedOn, year.s+"-")) {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, lawSummary(l))
	}
	return out, nil
}

// lawTitleSearch is the Arabic title search: codes first, then the newest.
func (s *Server) lawTitleSearch(ctx context.Context, match string, year yearVal, num string, limit int) ([]lawRow, error) {
	var where []string
	var params []any
	if match != "" {
		q, ok := parseFTS(match).usable()
		if !ok {
			return nil, nil
		}
		if pf := q.prefilter("ar_norm(l.title)", "ILIKE", &params); pf != "" {
			where = append(where, pf)
		}
		where = append(where, q.where("to_tsvector('simple', ar_norm(l.title))", "l.title", &params))
	}
	if year.positive() {
		params = append(params, year.s+"-%")
		where = append(where, fmt.Sprintf("to_char(l.issued_on, 'YYYY-MM-DD') LIKE $%d", len(params)))
	}
	if num != "" {
		params = append(params, num)
		where = append(where, fmt.Sprintf("l.number = $%d", len(params)))
	}
	params = append(params, limit)
	sql := `SELECT ` + lawCols + ` FROM ` + lawFrom + ` WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY (c.slug IS NOT NULL) DESC, l.issued_on DESC NULLS LAST, l.id LIMIT $%d`, len(params))
	rows, err := s.db.Query(ctx, sql, params...)
	if err != nil {
		return nil, err
	}
	return scanLaws(rows, nil)
}

func (s *Server) countLawTitles(ctx context.Context, match string, cap int) (int, error) {
	q, ok := parseFTS(match).usable()
	if !ok {
		return 0, nil
	}
	var params []any
	where := []string{}
	if pf := q.prefilter("ar_norm(l.title)", "ILIKE", &params); pf != "" {
		where = append(where, pf)
	}
	where = append(where, q.where("to_tsvector('simple', ar_norm(l.title))", "l.title", &params))
	params = append(params, cap)
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM legislation l WHERE `+strings.Join(where, " AND ")+
		fmt.Sprintf(` LIMIT $%d) x`, len(params)), params...).Scan(&n)
	return n, err
}

// fts5French builds a MATCH for the French texts: every word, as a prefix
// once it is long enough, with a plural "s" dropped ("obligations" finds
// "obligation" and "obligations").
func fts5French(q string) string {
	var out []string
	for _, w := range nonAlnum.Split(strings.ToLower(stripMarks(q)), -1) {
		if len(w) <= 1 {
			continue
		}
		if len(w) >= 5 && strings.HasSuffix(w, "s") {
			w = w[:len(w)-1]
		}
		if len(w) >= 4 {
			out = append(out, fts5Quote(w)+"*")
		} else {
			out = append(out, fts5Quote(w))
		}
	}
	return strings.Join(out, " AND ")
}

var (
	nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)
	enStop   = setOf("the", "of", "a", "an", "in", "and", "on", "for", "to", "by", "with", "is", "are")
)

// fts5English builds a MATCH for the English titles: every content word, as
// a prefix once it is long enough, with a plural "s" dropped.
func fts5English(q string) string {
	var out []string
	for _, t := range latinTokens(q) {
		for _, w := range nonAlnum.Split(t, -1) {
			if len(w) <= 1 || enStop[w] {
				continue
			}
			if len(w) >= 5 && strings.HasSuffix(w, "s") {
				w = w[:len(w)-1]
			}
			if len(w) >= 3 {
				out = append(out, fts5Quote(w)+"*")
			} else {
				out = append(out, fts5Quote(w))
			}
		}
	}
	return strings.Join(out, " AND ")
}

// phraseRows is how many rows of a corpus hold each phrase (cached).
func (s *Server) phraseRows(ctx context.Context, corpus string, phrases []ftsPhrase, count func(context.Context, ftsPhrase) (float64, error)) ([]float64, error) {
	out := make([]float64, len(phrases))
	for i, p := range phrases {
		key := corpus + "\x00" + p.tsquery()
		n, err := s.stats.rowsWith(ctx, key, func(ctx context.Context) (float64, error) { return count(ctx, p) })
		if err != nil {
			return nil, err
		}
		out[i] = n
	}
	return out, nil
}

// ---------------------------------------------------------------- reading a question again

type reading struct {
	query string
	num   string
	year  int
	kind  string // "translated" or "adjusted"
}

// rescueReadings lists, cheapest first, the other ways to read a question
// that found nothing: Latin terms to Arabic through the glossary, a number
// or a year in the question used as a filter on the texts, Latin names in
// other spellings against the English titles, a common leading word set
// aside. The caller uses the first that finds anything and says so.
func rescueReadings(q string) []reading {
	num, year, words := splitNumbers(q)
	var out []reading
	seen := map[string]bool{q + "||": true}
	add := func(query, n string, y int, kind string) {
		query = jsTrim(query)
		key := query + "|" + n + "|" + strconv.Itoa(y)
		if (query == "" && n == "" && y == 0) || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, reading{query, n, y, kind})
	}
	latinQ := !hasArabic(q)
	ar := words
	kind := "adjusted"
	if latinQ {
		g, _ := glossQueryWhole(strings.Join(words, " "))
		if g != "" {
			ar = jsFields(g)
			if num == "" && year == 0 {
				kind = "translated"
			}
		} else {
			ar = nil
		}
	}
	if len(ar) > 0 || num != "" || year != 0 {
		add(strings.Join(ar, " "), num, year, kind)
	}
	if len(ar) > 0 && (num != "" || year != 0) {
		add(strings.Join(ar, " "), "", 0, "adjusted")
	}
	// a year with fewer words, or the first word (often the kind of text, "قانون") set aside
	if len(ar) > 1 {
		if year != 0 && num == "" {
			add(strings.Join(ar[:len(ar)-1], " "), "", year, "adjusted")
		}
		add(strings.Join(ar[1:], " "), "", 0, "adjusted")
	}
	// names: other spellings of each Latin word, when the glossary knew none of them
	if latinQ && len(ar) == 0 {
		top := words
		if len(top) > 3 {
			top = top[:3]
		}
		for _, w := range top {
			vs := nameVariants(w)
			if len(vs) > 1 {
				for _, v := range vs[1:] {
					repl := make([]string, len(words))
					for i, x := range words {
						if x == w {
							repl[i] = v
						} else {
							repl[i] = x
						}
					}
					add(strings.Join(repl, " "), "", 0, "adjusted")
				}
			}
		}
	}
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}
