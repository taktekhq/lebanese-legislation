package mcpserver

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// articleHit is one article a search found: the law (lawCols), the
// article's number and the text its snippet comes from (the Arabic, or the
// French for a French match).
type articleHit struct {
	law    lawRow
	lawID  int
	number string
	text   string
}

type codeRef struct {
	id      int
	title   string
	numbers []string // article numbers the question names ("المادة 257", "670 671")
	rest    string   // the question less the code's name, the article words and the numbers
}

// Short names the codes go by besides their titles.
var codeAliases = map[string][]string{
	"civil-procedure":    {"اصول مدنيه"}, // "257 أصول مدنية"
	"criminal-procedure": {"اصول جزائيه"},
}

// codeWords are words of a code's name that say nothing on their own, as
// written (after arNorm), not stemmed: lightStem("قانون") is "قان".
var (
	codeWords    = setOf("قانون", "القانون", "نظام", "النظام", "اللبناني", "الجديد", "جديد") // "قانون السير" is «قانون السير الجديد»
	articleWords = setOf("الماده", "ماده", "المواد", "مواد", "م")
	codeNumber   = regexp.MustCompile(`^\d{1,4}(?:[-/]\d{1,3})?$`)
	yearLike     = regexp.MustCompile(`^(?:18|19|20)\d\d$`)
)

type codeName struct {
	id    int
	title string
	slug  string
}

// codeList caches the codes' ids, titles and slugs (fresh.go).
type codeList struct{ f fresh[[]codeName] }

func (s *Server) codes(ctx context.Context) ([]codeName, error) {
	return s.codeCache.f.get(ctx, s.loadCodes)
}

func (s *Server) loadCodes(ctx context.Context) ([]codeName, error) {
	rows, err := s.db.Query(ctx, `SELECT l.id, l.title, c.slug FROM `+lawFrom+` WHERE c.slug IS NOT NULL ORDER BY l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	codes := []codeName{}
	for rows.Next() {
		var c codeName
		if err := rows.Scan(&c.id, &c.title, &c.slug); err != nil {
			return nil, err
		}
		codes = append(codes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return codes, nil
}

// codeIn finds a code named in an Arabic question, however it is written:
// "أصول محاكمات مدنية" and "قانون اصول المحاكمات المدنية" are both the Code
// of Civil Procedure, "موجبات وعقود" the Code of Obligations. Every word of
// the name must be there; the longest name wins ("قانون العقوبات العسكري"
// over "قانون العقوبات").
func (s *Server) codeIn(ctx context.Context, q string) (*codeRef, error) {
	if !hasArabic(q) {
		return nil, nil
	}
	codes, err := s.codes(ctx)
	if err != nil {
		return nil, err
	}
	var words, stems []string
	for _, w := range jsFields(arNorm(q)) {
		if w = trimEdges(w); w != "" {
			words = append(words, w)
			stems = append(stems, lightStem(w))
		}
	}
	has := setOf(stems...)
	var best *codeName
	var bestName []string
	for i := range codes {
		c := &codes[i]
		for _, n := range append([]string{c.title}, codeAliases[c.slug]...) {
			var name []string
			for _, w := range jsFields(arNorm(n)) {
				if !codeWords[w] && !arStop[w] {
					name = append(name, lightStem(w))
				}
			}
			all := len(name) > 0
			for _, st := range name {
				if !has[st] {
					all = false
					break
				}
			}
			if all && (best == nil || len(name) > len(bestName)) {
				best, bestName = c, name
			}
		}
	}
	if best == nil {
		return nil, nil
	}
	name := setOf(bestName...)
	ref := &codeRef{id: best.id, title: best.title}
	var rest []string
	for i, w := range words {
		if codeNumber.MatchString(w) && !yearLike.MatchString(w) {
			ref.numbers = append(ref.numbers, strings.Replace(w, "/", "-", 1))
		} else if !name[stems[i]] && !codeWords[w] && !articleWords[w] && !arStop[w] {
			rest = append(rest, w)
		}
	}
	ref.rest = strings.Join(rest, " ")
	return ref, nil
}

func (s *Server) findArticles(ctx context.Context, q string, limit int, forms bool) ([]any, string, error) {
	var hits []articleHit
	add := func(rows []articleHit) {
		for _, r := range rows {
			if len(hits) >= limit {
				return
			}
			dup := false
			for _, h := range hits {
				if h.lawID == r.lawID && h.number == r.number {
					dup = true
					break
				}
			}
			if !dup {
				hits = append(hits, r)
			}
		}
	}
	// A code named in the question: the articles it numbers, then its own
	// articles holding the other words. "قانون العقوبات 670 671" found no
	// article (no article says «قانون العقوبات»), nor did "المادة 257 أصول
	// محاكمات مدنية صورية".
	words := q
	code, err := s.codeIn(ctx, q)
	if err != nil {
		return nil, "", err
	}
	if code != nil {
		if len(code.numbers) > 0 {
			rows, err := s.articleHits(ctx, `a.law_id = $1 AND a.number = ANY($2) ORDER BY a.position`, code.id, code.numbers)
			if err != nil {
				return nil, "", err
			}
			for _, n := range code.numbers {
				var same []articleHit
				for _, r := range rows {
					if r.number == n {
						same = append(same, r)
					}
				}
				add(same)
			}
		}
		if code.rest != "" {
			for _, m := range []string{fts5MatchAll(code.rest), fts5Forms(code.rest)} {
				if m == "" || len(hits) >= limit {
					continue
				}
				before := len(hits)
				rows, err := s.articleMatch(ctx, m, limit, code.id)
				if err != nil {
					return nil, "", err
				}
				add(rows)
				if len(hits) > before {
					break
				}
			}
			words = code.rest
		}
		if len(hits) > 0 {
			out, err := s.articleOut(ctx, hits)
			return out, "", err
		}
	}
	notInCode := ""
	if code != nil {
		notInCode = code.title
	}
	m := fts5MatchAll(words)
	if forms {
		m = fts5Forms(words)
	}
	if m != "" {
		rows, err := s.articleMatch(ctx, m, limit, 0)
		if err != nil {
			return nil, "", err
		}
		add(rows)
	}
	if hasLatin(q) && len(hits) < limit {
		if fr := fts5French(q); fr != "" {
			rows, err := s.frenchMatch(ctx, fr, limit-len(hits))
			if err != nil {
				return nil, "", err
			}
			add(rows)
		}
	}
	out, err := s.articleOut(ctx, hits)
	return out, notInCode, err
}

// articleHits reads hits by a condition on a (the article) and l (its law).
func (s *Server) articleHits(ctx context.Context, where string, params ...any) ([]articleHit, error) {
	rows, err := s.db.Query(ctx, `SELECT `+lawCols+`, a.law_id, a.number, a.text_ar FROM legislation_article a JOIN legislation l ON l.id = a.law_id `+lawJoins+` WHERE `+where, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []articleHit
	for rows.Next() {
		var h articleHit
		if err := rows.Scan(append(h.law.dest(), &h.lawID, &h.number, &h.text)...); err != nil {
			return nil, err
		}
		h.text = scanReadings(h.text)
		out = append(out, h)
	}
	return out, rows.Err()
}

// candidate is an article a MATCH found, with what bm25 needs.
type candidate struct {
	lawID    int
	position int
	code     bool
	dl       float64
	tf       []float64
}

const articleMatchFrom = `FROM legislation_article a JOIN legislation l ON l.id = a.law_id JOIN legislation_kind k ON k.name_ar = l.kind
	LEFT JOIN code c ON c.law_id = a.law_id`

func (s *Server) candidates(ctx context.Context, vec, text, where string, params []any, phrases []ftsPhrase) ([]candidate, error) {
	tfs := tfColumns(phrases, vec, text, &params)
	rows, err := s.db.Query(ctx, `SELECT a.law_id, a.position, c.law_id IS NOT NULL, `+tfs+` `+articleMatchFrom+` WHERE `+where, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		var st []float64
		if err := rows.Scan(&c.lawID, &c.position, &c.code, &st); err != nil {
			return nil, err
		}
		c.dl, c.tf = st[0], st[1:]
		out = append(out, c)
	}
	return out, rows.Err()
}

// rank orders candidates as the Worker's queries did: codes first, then the
// best bm25, ties in the snapshot's row order.
func rank(cands []candidate, st corpusStats, df []float64, limit int) []candidate {
	scores := make(map[[2]int]float64, len(cands))
	for _, c := range cands {
		scores[[2]int{c.lawID, c.position}] = bm25(st, df, c.tf, c.dl)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.code != b.code {
			return a.code
		}
		sa, sb := scores[[2]int{a.lawID, a.position}], scores[[2]int{b.lawID, b.position}]
		if sa != sb {
			return sa > sb
		}
		if a.lawID != b.lawID {
			return a.lawID < b.lawID
		}
		return a.position < b.position
	})
	if len(cands) > limit {
		cands = cands[:limit]
	}
	return cands
}

// articleMatch runs one MATCH on the Arabic articles, codes first, then the
// best match (bm25); lawID keeps it to one text.
func (s *Server) articleMatch(ctx context.Context, match string, limit, lawID int) ([]articleHit, error) {
	q, ok := parseFTS(match).usable()
	if !ok {
		return nil, nil
	}
	phrases := q.phrases()
	var params []any
	where := q.where("a.search_exact", "a.text_ar", &params)
	if lawID != 0 {
		params = append(params, lawID)
		where += fmt.Sprintf(` AND a.law_id = $%d`, len(params))
	}
	// Codes rank first: when they alone fill the page, the others need no score.
	cands, err := s.candidates(ctx, "a.search_exact", "a.text_ar", where+` AND c.law_id IS NOT NULL`, params, phrases)
	if err != nil {
		return nil, err
	}
	if len(cands) < limit {
		more, err := s.candidates(ctx, "a.search_exact", "a.text_ar", where+` AND c.law_id IS NULL`, params, phrases)
		if err != nil {
			return nil, err
		}
		cands = append(cands, more...)
	}
	// The snapshot indexed the calculated current amounts beside the
	// Civil Procedure articles' text; no Postgres vector holds them.
	if lawID == 0 || lawID == calculatedLaw {
		extra, err := s.calculatedIndex(ctx)
		if err != nil {
			return nil, err
		}
		have := map[int]bool{}
		for _, c := range cands {
			if c.lawID == calculatedLaw {
				have[c.position] = true
			}
		}
		for _, e := range extra {
			if have[e.position] || !q.matches(e.tokens) {
				continue
			}
			c := candidate{lawID: calculatedLaw, position: e.position, code: e.code, dl: float64(len(e.tokens))}
			for _, p := range phrases {
				c.tf = append(c.tf, float64(p.tf(e.tokens)))
			}
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		return nil, nil
	}
	st, err := s.stats.corpus(ctx, "articles", s.articleStats)
	if err != nil {
		return nil, err
	}
	df, err := s.phraseRows(ctx, "articles", phrases, func(ctx context.Context, p ftsPhrase) (float64, error) {
		var ps []any
		w := ftsQuery{groups: [][]ftsPhrase{{p}}}.where("a.search_exact", "a.text_ar", &ps)
		var n float64
		err := s.db.QueryRow(ctx, `SELECT count(*)::float8 FROM legislation_article a WHERE `+w, ps...).Scan(&n)
		return n, err
	})
	if err != nil {
		return nil, err
	}
	return s.hitsFor(ctx, rank(cands, st, df, limit), "a.text_ar")
}

// hitsFor reads the hits for ranked candidates, in their order.
func (s *Server) hitsFor(ctx context.Context, cands []candidate, textCol string) ([]articleHit, error) {
	if len(cands) == 0 {
		return nil, nil
	}
	laws := make([]int32, len(cands))
	pos := make([]int32, len(cands))
	for i, c := range cands {
		laws[i], pos[i] = int32(c.lawID), int32(c.position)
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawCols+`, a.law_id, a.position, a.number, coalesce(`+textCol+`, '')
		FROM legislation_article a JOIN legislation l ON l.id = a.law_id `+lawJoins+`
		WHERE (a.law_id, a.position) IN (SELECT * FROM unnest($1::int[], $2::int[]))`, laws, pos)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byKey := map[[2]int]articleHit{}
	for rows.Next() {
		var h articleHit
		var p int
		if err := rows.Scan(append(h.law.dest(), &h.lawID, &p, &h.number, &h.text)...); err != nil {
			return nil, err
		}
		if textCol == "a.text_ar" {
			h.text = scanReadings(h.text)
		}
		byKey[[2]int{h.lawID, p}] = h
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]articleHit, 0, len(cands))
	for _, c := range cands {
		if h, ok := byKey[[2]int{c.lawID, c.position}]; ok {
			out = append(out, h)
		}
	}
	return out, nil
}

func (s *Server) articleStats(ctx context.Context) (corpusStats, error) {
	var st corpusStats
	if err := s.db.QueryRow(ctx, `SELECT count(*)::float8 FROM legislation_article`).Scan(&st.rows); err != nil {
		return st, err
	}
	// The average length from a fixed sample: a full pass reads every article.
	err := s.db.QueryRow(ctx, `SELECT coalesce(avg((SELECT sum(cardinality(u.positions)) FROM unnest(a.search_exact) u)), 0)::float8
		FROM legislation_article a TABLESAMPLE SYSTEM (3) REPEATABLE (1)`).Scan(&st.avgLen)
	return st, err
}

func (s *Server) countArticles(ctx context.Context, match string, cap int) (int, error) {
	q, ok := parseFTS(match).usable()
	if !ok {
		return 0, nil
	}
	var ps []any
	w := q.where("a.search_exact", "a.text_ar", &ps)
	ps = append(ps, cap)
	var n int
	err := s.db.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM (SELECT 1 FROM legislation_article a WHERE %s LIMIT $%d) x`, w, len(ps)), ps...).Scan(&n)
	return n, err
}

// calculatedLaw is the Code of Civil Procedure, the one text with
// calculated current amounts.
const calculatedLaw = 244565

type calculatedEntry struct {
	position int
	code     bool
	tokens   []string
}

type calcIndex struct{ f fresh[[]calculatedEntry] }

// calculatedIndex is, for every article with calculated amounts, the
// tokens the snapshot indexed: its text and the calculated text. Read once
// and reloaded in the background (fresh.go): every article search needs it.
func (s *Server) calculatedIndex(ctx context.Context) ([]calculatedEntry, error) {
	return s.calcCache.f.get(ctx, s.loadCalculatedIndex)
}

func (s *Server) loadCalculatedIndex(ctx context.Context) ([]calculatedEntry, error) {
	rows, err := s.queryArticles(ctx, `a.law_id = $1 ORDER BY a.position`, calculatedLaw)
	if err != nil {
		return nil, err
	}
	codes, err := s.codes(ctx)
	if err != nil {
		return nil, err
	}
	isCode := false
	for _, c := range codes {
		if c.id == calculatedLaw {
			isCode = true
		}
	}
	entries := []calculatedEntry{}
	for _, a := range rows {
		if a.Calculated == nil {
			continue
		}
		var rec struct {
			TextAr string `json:"text_ar"`
		}
		if err := jsonUnmarshal(*a.Calculated, &rec); err != nil {
			continue
		}
		entries = append(entries, calculatedEntry{a.Position, isCode, ftsTokens(arNorm(a.TextAr) + " " + arNorm(rec.TextAr))})
	}
	return entries, nil
}

func (s *Server) articleOut(ctx context.Context, hits []articleHit) ([]any, error) {
	out := []any{}
	today := s.today()
	for i := range hits {
		h := &hits[i]
		o := obj{{"law", lawSummary(&h.law)}, {"article_number", h.number}, {"snippet", snippet(h.text)}}
		if h.lawID == calculatedLaw {
			a, err := s.firstArticle(ctx, int64(h.lawID), h.number)
			if err != nil {
				return nil, err
			}
			if c := calculatedCurrent(a, h.law.Status, today); c != nil {
				o.set("calculated_current", c)
			}
		}
		o.set("url", articleURL(h.lawID, h.number, h.law.Kind))
		out = append(out, o)
	}
	return out, nil
}

// snippet is the start of a text, cut at 220 characters.
func snippet(text string) string {
	const maxChars = 220
	t := jsTrim(text)
	if jsLen(t) <= maxChars {
		return t
	}
	return jsTrimEnd(jsSlice(t, maxChars)) + "…"
}

// ---------------------------------------------------------------- rulings and the Gazette

func (s *Server) findRulings(ctx context.Context, q string, limit int, forms bool) ([]any, error) {
	m := fts5MatchAll(q)
	if forms {
		m = fts5Forms(q)
	}
	out := []any{}
	if m == "" {
		return out, nil
	}
	fq, ok := parseFTS(m).usable()
	if !ok {
		return out, nil
	}
	var params []any
	w := fq.where("r.search_exact", rulingText, &params)
	params = append(params, limit)
	rows, err := s.db.Query(ctx, `SELECT r.id, r.title, r.court, to_char(r.decided_on, 'YYYY-MM-DD'), r.text_ar,
		EXISTS (SELECT 1 FROM ruling_file rf WHERE rf.ruling_id = r.id)
		FROM ruling r WHERE `+w+fmt.Sprintf(` ORDER BY r.decided_on DESC NULLS LAST, r.id LIMIT $%d`, len(params)), params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var title string
		var court, decided, text *string
		var file bool
		if err := rows.Scan(&id, &title, &court, &decided, &text, &file); err != nil {
			return nil, err
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
	return out, rows.Err()
}

// rulingText is what a ruling's search vector reads: its title and text.
const rulingText = "(r.title || ' ' || coalesce(r.text_ar, ''))"

// pdfURL is the judgment's scan on the Lebanese University's site.
func pdfURL(id int) string {
	return fmt.Sprintf("https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=%d&type=list", id)
}

func (s *Server) countRulings(ctx context.Context, match string, cap int) (int, error) {
	q, ok := parseFTS(match).usable()
	if !ok {
		return 0, nil
	}
	var ps []any
	w := q.where("r.search_exact", rulingText, &ps)
	ps = append(ps, cap)
	var n int
	err := s.db.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM (SELECT 1 FROM ruling r WHERE %s LIMIT $%d) x`, w, len(ps)), ps...).Scan(&n)
	return n, err
}

var (
	gazNum     = regexp.MustCompile(`^\d{1,6}$`)
	gazWord    = regexp.MustCompile(`(?i)جريد|gazette|journal|عدد`)
	gazAnyNum  = regexp.MustCompile(`\d{1,6}`)
	gazYear    = regexp.MustCompile(`^\d{4}$`)
	gazNumYear = regexp.MustCompile(`^(\d{1,4})[` + jsSpace + `]*[/\-][` + jsSpace + `]*((?:19|20)\d\d)$`)
)

// gazetteNumber is the issue number a question asks for: the question is a
// number, or names the Gazette and a number ("الجريدة الرسمية عدد 39").
// Anything else names no issue.
func gazetteNumber(q string) string {
	t := jsTrim(foldDigits(q))
	if gazNum.MatchString(t) {
		return t
	}
	if !gazWord.MatchString(t) {
		return ""
	}
	return gazAnyNum.FindString(t)
}

// gazetteQuery reads what a Gazette search asks for: a bare year ("2025",
// 1900 to 2100) is the issues of that year, "22/2025" is issue 22 of 2025,
// anything else is an issue number as gazetteNumber reads it.
func gazetteQuery(q string) (num string, year int) {
	t := jsTrim(foldDigits(q))
	if gazYear.MatchString(t) {
		if y := atoi(t); y >= 1900 && y <= 2100 {
			return "", y
		}
	}
	if m := gazNumYear.FindStringSubmatch(t); m != nil {
		return itoa(atoi(m[1])), atoi(m[2])
	}
	return gazetteNumber(q), 0
}

func (s *Server) findGazette(ctx context.Context, q string, limit int, yearArg yearVal) ([]any, int, error) {
	num, pyear := gazetteQuery(q)
	year := pyear
	if year == 0 && yearArg.truthy && yearArg.f >= 1900 && yearArg.f <= 2100 {
		year = int(truncf(yearArg.f))
	}
	out := []any{}
	if num == "" && year == 0 {
		return out, 0, nil
	}
	var where []string
	var params []any
	if num != "" {
		// a year in the question makes the number exact: issue 22 of 2025
		if pyear != 0 {
			params = append(params, num)
			where = append(where, fmt.Sprintf("number = $%d", len(params)))
		} else {
			params = append(params, "%"+likeEscape(num)+"%")
			where = append(where, fmt.Sprintf("number LIKE $%d", len(params)))
		}
	}
	if year != 0 {
		params = append(params, fmt.Sprintf("%d-01-01", year), fmt.Sprintf("%d-12-31", year))
		where = append(where, fmt.Sprintf("published_on >= $%d::date AND published_on <= $%d::date", len(params)-1, len(params)))
	}
	w := strings.Join(where, " AND ")
	var count int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM gazette_issue WHERE `+w, params...).Scan(&count); err != nil {
		return nil, 0, err
	}
	params = append(params, limit)
	rows, err := s.db.Query(ctx, `SELECT id, number, to_char(published_on, 'YYYY-MM-DD') FROM gazette_issue WHERE `+w+
		fmt.Sprintf(` ORDER BY published_on DESC NULLS LAST, id DESC LIMIT $%d`, len(params)), params...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var number, published *string
		if err := rows.Scan(&id, &number, &published); err != nil {
			return nil, 0, err
		}
		g := obj{{"id", id}}
		g.str("number", number)
		g.str("published_on", published)
		g.set("url", fmt.Sprintf("%s/gazette/%d/", Base, id))
		out = append(out, g)
	}
	return out, count, rows.Err()
}
