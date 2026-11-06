package mcpserver

import (
	"context"
	"fmt"
	"regexp"
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

// codes are the codes' ids, titles and slugs (26 rows, read when a question
// is checked for a code's name).
func (s *Server) codes(ctx context.Context) ([]codeName, error) {
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

// findArticles are the articles a question asks for: the numbered articles
// of a code it names, then the articles that hold its words (in that code
// when it names one), ranked in one query (search_pg.go), then the French
// texts for a Latin question. full counts the matches holding every word.
func (s *Server) findArticles(ctx context.Context, q string, limit int) (out []any, notInCode string, full int, err error) {
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
		return nil, "", 0, err
	}
	if code != nil {
		if len(code.numbers) > 0 {
			rows, err := s.articleHits(ctx, `a.law_id = $1 AND a.number = ANY($2) ORDER BY a.position`, code.id, code.numbers)
			if err != nil {
				return nil, "", 0, err
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
		if code.rest != "" && len(hits) < limit {
			rows, f, err := s.rankedArticles(ctx, code.rest, false, code.id, limit)
			if err != nil {
				return nil, "", 0, err
			}
			add(rows)
			full += f
			words = code.rest
		}
		if len(hits) > 0 {
			out, err := s.articleOut(ctx, hits)
			return out, "", full, err
		}
		notInCode = code.title
	}
	rows, f, err := s.rankedArticles(ctx, words, false, 0, limit)
	if err != nil {
		return nil, "", 0, err
	}
	add(rows)
	full += f
	if hasLatin(q) && len(hits) < limit {
		rows, f, err := s.rankedArticles(ctx, q, true, 0, limit-len(hits))
		if err != nil {
			return nil, "", 0, err
		}
		add(rows)
		full += f
	}
	out, err = s.articleOut(ctx, hits)
	return out, notInCode, full, err
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

// candidate is a ranked article (search_pg.go), before it is read in full.
type candidate struct {
	lawID    int
	position int
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

// calculatedLaw is the Code of Civil Procedure, the one text with
// calculated current amounts.
const calculatedLaw = 244565

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

// findRulings are the rulings whose title or text holds the question's
// words, ranked in one query (search_pg.go).
func (s *Server) findRulings(ctx context.Context, q string, limit int) ([]any, int, error) {
	return s.rankedRulings(ctx, q, limit)
}

// pdfURL is the judgment's scan on the Lebanese University's site.
func pdfURL(id int) string {
	return fmt.Sprintf("https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=%d&type=list", id)
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
