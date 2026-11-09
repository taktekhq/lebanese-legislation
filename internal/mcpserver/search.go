package mcpserver

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
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

// found is the four searches, in the Worker's field order.
type found struct {
	o     obj
	total int
	full  int // results that hold every word of the question
}

// searchAll runs the four searches one after another, each one ranked
// query (search_pg.go): a question that no text matches word for word
// still finds the texts holding its rarest words, with no second pass.
// They ran at once until 9 Nov 2026; on the one CPU the origin has, that
// saved nothing and cost more: at the live limits (docker --cpus=1) the
// 30 benchmark questions' p95 was 89 to 93 ms at once, 44 ms in turn.
// num narrows laws by their own number; with lawsOnly (a number or a year
// names a text, not an article or a ruling) only the laws are searched.
func (s *Server) searchAll(ctx context.Context, q, kind string, year yearVal, limit int, num string, lawsOnly bool) (found, error) {
	want := func(k string) bool { return kind == "" || kind == k }
	type part struct {
		set   func(*obj)
		total int
		full  int
		err   error
	}
	var jobs []func() part
	if want("laws") {
		jobs = append(jobs, func() part {
			rows, full, err := s.findLaws(ctx, q, year, limit, num)
			return part{func(o *obj) { o.set("laws", rows); o.set("law_count", len(rows)) }, len(rows), full, err}
		})
	}
	if want("articles") && !lawsOnly {
		jobs = append(jobs, func() part {
			rows, notInCode, full, err := s.findArticles(ctx, q, limit)
			return part{func(o *obj) {
				o.set("articles", rows)
				o.set("article_count", len(rows))
				if notInCode != "" && len(rows) > 0 {
					o.set("articles_not_in_code", notInCode)
				}
			}, len(rows), full, err}
		})
	}
	if want("rulings") && !lawsOnly {
		jobs = append(jobs, func() part {
			rows, full, err := s.findRulings(ctx, q, limit)
			return part{func(o *obj) { o.set("rulings", rows); o.set("ruling_count", len(rows)) }, len(rows), full, err}
		})
	}
	if want("gazette") && !lawsOnly {
		jobs = append(jobs, func() part {
			rows, count, err := s.findGazette(ctx, q, limit, year)
			return part{func(o *obj) { o.set("gazette", rows); o.set("gazette_count", count) }, count, count, err}
		})
	}
	var f found
	for _, job := range jobs { // in the Worker's field order
		p := job()
		if p.err != nil {
			return f, p.err
		}
		p.set(&f.o)
		f.total += p.total
		f.full += p.full
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
	f, err := s.searchAll(ctx, q, kind, year, limit, "", false)
	if err != nil {
		return toolResult{}, err
	}
	if f.full == 0 && kind != "gazette" {
		// No text holds every word as written. A citation ("law 44 of 2015")
		// or an English question the glossary turns into Arabic words reads
		// differently: one more ranked pass with that reading, if it finds
		// texts holding all its words. When nothing at all was found, the
		// other readings too: a Latin name in another spelling, the first
		// word (often the kind of text) set aside. Each is one pass, and
		// they stop at the first that finds texts holding all its words.
		for _, alt := range rescueReadings(q) {
			if f.total > 0 && alt.kind != "translated" && alt.num == "" && alt.year == 0 {
				continue
			}
			named := alt.num != "" || alt.year != 0
			y := year
			if alt.year != 0 {
				y = yearInt(alt.year)
			}
			r, err := s.searchAll(ctx, alt.query, kind, y, limit, alt.num, named && (kind == "" || kind == "laws"))
			if err != nil {
				return toolResult{}, err
			}
			if r.full > 0 {
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
	if f.full == 0 && f.total > 0 {
		// tell the agent these hold only some of the words, rarest first
		out.set("partial_match", true)
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

// findLaws are the laws a question names: a pinned text, a law's id, a code
// by its English or French name, then the titles that hold its words. full
// counts the title matches that hold every word.
func (s *Server) findLaws(ctx context.Context, q string, year yearVal, limit int, num string) (out []any, full int, err error) {
	var foundRows []lawRow
	// A question that names one text ("cybercrime" is Law 81 of 2018) has it first (pins in gloss.json).
	if num == "" {
		if pin := pinnedLaw(q); pin != 0 {
			l, err := s.loadLaw(ctx, int64(pin))
			if err != nil {
				return nil, 0, err
			}
			if l != nil {
				foundRows = append(foundRows, *l)
			}
		}
	}
	// A citation ("قانون رقم 44 تاريخ 2015", "law 81/2018"): the text with that
	// number, of that year, first.
	if cn, cy, _ := splitNumbers(q); num == "" && cn != "" && cy != 0 {
		rows, _, err := s.rankedLaws(ctx, "", false, yearInt(cy), cn, limit)
		if err != nil {
			return nil, 0, err
		}
		foundRows = append(foundRows, rows...)
	}
	// A law's id, as get_law and the site's addresses use it.
	if num == "" && lawIDRe.MatchString(q) {
		id, _ := strconv.ParseInt(q, 10, 64)
		l, err := s.loadLaw(ctx, id)
		if err != nil {
			return nil, 0, err
		}
		if l != nil {
			foundRows = append(foundRows, *l)
		}
	}
	// The codes by their English or French name ("Penal Code", "obligations"),
	// closest name first.
	if num == "" && hasLatin(q) {
		rows, err := s.db.Query(ctx, `SELECT `+s.sql(ctx).lawCols+` FROM `+s.sql(ctx).lawFrom+` WHERE c.slug IS NOT NULL ORDER BY l.id`)
		if err != nil {
			return nil, 0, err
		}
		codes, err := scanLaws(rows, nil)
		if err != nil {
			return nil, 0, err
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
	// The Arabic titles (official), then the unofficial English ones, each
	// ranked in one query (search_pg.go).
	rows, full, err := s.rankedLaws(ctx, titleQ, false, year, num, limit)
	if err != nil {
		return nil, 0, err
	}
	foundRows = append(foundRows, rows...)
	fullTotal := full
	if hasLatin(q) && num == "" {
		rows, full, err := s.rankedLaws(ctx, titleQ, true, year, "", limit)
		if err != nil {
			return nil, 0, err
		}
		foundRows = append(foundRows, rows...)
		fullTotal += full
	}
	seen := map[int]bool{}
	out = []any{}
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
	return out, fullTotal, nil
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

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
