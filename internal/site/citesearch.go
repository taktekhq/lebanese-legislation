package site

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// findLawCite finds the text a citation names (readQuery). It tries the most
// exact reading first: number and full date, number and year, the date alone
// (with a title word if the query has any), the number alone. The query's
// other words only rank. It reports false when nothing matched, so the page
// can say the text is not in the database instead of showing near misses.
func (s *Server) findLawCite(ctx context.Context, res *Results, r queryRead) (bool, error) {
	type tier struct {
		cond string
		args []any
		off  bool // the citation is read with a part left out: the page says what it found instead
	}
	var tiers []tier
	yearCond := func(n int) string {
		return fmt.Sprintf("l.issued_on >= make_date($%[1]d, 1, 1) AND l.issued_on < make_date($%[1]d + 1, 1, 1)", n)
	}
	date := r.Date.Format("2006-01-02")
	switch {
	case r.Num > 0 && !r.Date.IsZero():
		tiers = append(tiers, tier{cond: "l.number_int = $1 AND l.issued_on = $2::date", args: []any{r.Num, date}},
			tier{cond: "l.number_int = $1 AND " + yearCond(2), args: []any{r.Num, r.Year}},
			// The year is off: "القانون 617 تاريخ 8/8/2012" is decree 617 of 8/8/2007 (23 Oct 2026).
			tier{cond: "l.number_int = $1 AND extract(month FROM l.issued_on) = $2 AND extract(day FROM l.issued_on) = $3",
				args: []any{r.Num, int(r.Date.Month()), r.Date.Day()}, off: true})
	case r.Num > 0 && r.Year > 0:
		tiers = append(tiers, tier{cond: "l.number_int = $1 AND " + yearCond(2), args: []any{r.Num, r.Year}})
	case r.Num > 0:
		tiers = append(tiers, tier{cond: "l.number_int = $1", args: []any{r.Num}})
	}
	// The kind and the date are off but the text it amends is named: "القانون
	// 617 ... المعدّل للقانون 15874" is the decree 617 whose title cites 15874.
	if r.Num > 0 && r.Year > 0 {
		for _, w := range r.Words {
			if n, err := strconv.Atoi(w); err == nil && n != r.Num && len(w) >= 3 && len(w) <= 5 {
				tiers = append(tiers, tier{cond: "l.number_int = $1 AND ar_norm(l.title) LIKE ar_norm($2)", args: []any{r.Num, "%" + w + "%"}, off: true})
			}
		}
	}
	if !r.Date.IsZero() && r.Num == 0 {
		if len(r.Words) > 0 { // "قانون الجنسية 31/1/1946": that day's texts whose title has one of the words
			var or []string
			args := []any{date}
			for _, w := range r.Words {
				args = append(args, "%"+w+"%")
				or = append(or, fmt.Sprintf("ar_norm(l.title) LIKE ar_norm($%d)", len(args)))
			}
			tiers = append(tiers, tier{cond: "l.issued_on = $1::date AND (" + strings.Join(or, " OR ") + ")", args: args})
			// The day may be off ("31/12/1946" for 31/1/1946): the words in that year.
			conds, wargs := titleConds(r.Words, 1)
			if len(conds) > 0 {
				wargs = append(wargs, r.Year)
				tiers = append(tiers, tier{cond: strings.Join(conds, " AND ") + " AND " + yearCond(len(wargs)), args: wargs})
			}
			// ... less the word that tells least: "قانون الغابات اللبناني 7/12/1949"
			// is the forest law of 7/1/1949, whose title has no «اللبناني».
			if v := s.vocab.Load(); len(r.Words) > 1 && v != nil {
				rest := dropOne(v, r.Words)
				if conds, wargs := titleConds(rest, 1); len(conds) > 0 && (len(rest) > 1 || wordWeight(v, rest[0]) <= 500) {
					wargs = append(wargs, r.Year)
					tiers = append(tiers, tier{cond: strings.Join(conds, " AND ") + " AND " + yearCond(len(wargs)), args: wargs})
				}
			}
		}
		tiers = append(tiers, tier{cond: "l.issued_on = $1::date", args: []any{date}})
	}
	for _, t := range tiers {
		var n int
		if err := s.db.QueryRow(ctx, "SELECT count(*) FROM legislation l WHERE "+t.cond, t.args...).Scan(&n); err != nil {
			return false, err
		}
		if n == 0 {
			continue
		}
		args := append(append([]any{}, t.args...), ownKinds(r.Kind), kindGroups[r.Kind], kindNear[r.Kind], strings.Join(r.Words, " "))
		own, k, near, q := len(args)-3, len(args)-2, len(args)-1, len(args)
		// After kind and title: a text of some length before a one-line notice
		// (articles counted up to 10, so among real texts the newest still wins).
		rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
			WHERE `+t.cond+fmt.Sprintf(` ORDER BY (l.kind = ANY($%[4]d)) DESC, (l.kind = ANY($%[1]d)) DESC, (l.kind = ANY($%[1]d) OR l.kind = ANY($%[2]d)) DESC, similarity(ar_norm(l.title), ar_norm($%[3]d)) DESC,
			(EXISTS (SELECT 1 FROM code c WHERE c.law_id = l.id)) DESC,
			(SELECT count(*) FROM (SELECT 1 FROM legislation_article a WHERE a.law_id = l.id LIMIT 10) a) DESC,
			l.issued_on DESC NULLS LAST, l.id DESC LIMIT 10`, k, near, q, own), args...)
		if err != nil {
			return false, err
		}
		if res.Laws, err = scanLawRows(rows); err != nil {
			return false, err
		}
		res.LawCount = n
		res.moreCited(false, t.cond, t.args)
		// The year is off and the title says so: "(2012) (القرار رقم 1/1 -
		// الشروط الواجب مراعاتها في المصانع والمستودعات الغذائية)" is decision 1
		// of 5/1/2015, and listed ten unrelated texts numbered 1 of 2012
		// (23 Oct 2026). A text with that number whose title is close to the
		// words comes first when none found is.
		if alt := s.closeTitleOtherYear(ctx, r, res.Laws); alt != nil {
			var added bool
			if res.Laws, added = withPin(res.Laws, alt, 10); added {
				res.LawCount++
			}
			t.off = true
		}
		if t.off && len(res.Laws) > 0 { // say which text this is: "617 08/08/2007"
			first := res.Laws[0]
			var p []string
			if first.Number != nil {
				p = append(p, *first.Number)
			}
			if first.IssuedOn != nil {
				p = append(p, first.IssuedOn.Format("02/01/2006"))
			}
			res.Via, res.ViaKind = strings.Join(p, " "), "adjusted"
		}
		return true, nil
	}
	return false, nil
}

// closeTitleOtherYear is the text with the cited number whose title is close
// to the citation's words (similarity 0.5 or more) when none of found comes
// within 0.25 of it; nil otherwise, or when the citation has fewer than three
// words to judge a title by.
func (s *Server) closeTitleOtherYear(ctx context.Context, r queryRead, found []LawRow) *LawRow {
	if r.Num == 0 || r.Year == 0 || len(r.Words) < 3 || len(found) == 0 {
		return nil
	}
	words := strings.Join(r.Words, " ")
	var best float64
	for _, l := range found {
		var sim float64
		if s.db.QueryRow(ctx, `SELECT similarity(ar_norm($1), ar_norm($2))`, l.Title, words).Scan(&sim) == nil && sim > best {
			best = sim
		}
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE l.number_int = $1 AND similarity(ar_norm(l.title), ar_norm($2)) >= 0.5
		  AND similarity(ar_norm(l.title), ar_norm($2)) > $3 + 0.25
		ORDER BY similarity(ar_norm(l.title), ar_norm($2)) DESC LIMIT 1`, r.Num, words, best)
	if err != nil {
		return nil
	}
	alt, err := scanLawRows(rows)
	if err != nil || len(alt) == 0 {
		return nil
	}
	return &alt[0]
}

// findRulingCite finds rulings by court and number or year: "قرار رقم 50/2002
// تمييز جزائي", "محكمة التمييز الجزائية 2006". Each court word must be in the
// court's name (ruling.court: «تمييز جزائي», «إستئناف مدني», «شورى»…).
func (s *Server) findRulingCite(ctx context.Context, res *Results, r queryRead) (bool, error) {
	var conds []string
	var args []any
	for _, c := range r.Court {
		args = append(args, "%"+c+"%")
		conds = append(conds, fmt.Sprintf("ar_norm(coalesce(court, '')) LIKE ar_norm($%d)", len(args)))
	}
	if r.Num > 0 {
		args = append(args, strconv.Itoa(r.Num))
		conds = append(conds, fmt.Sprintf("number = $%d", len(args)))
	}
	if r.Year > 0 {
		args = append(args, r.Year)
		conds = append(conds, fmt.Sprintf("year = $%d", len(args)))
	}
	if !r.Date.IsZero() {
		args = append(args, r.Date)
		conds = append(conds, fmt.Sprintf("decided_on = $%d", len(args)))
	}
	cond := strings.Join(conds, " AND ")
	if err := s.db.QueryRow(ctx, "SELECT count(*) FROM (SELECT 1 FROM ruling WHERE "+cond+" LIMIT 1000) t", args...).Scan(&res.RulCount); err != nil {
		return false, err
	}
	if res.RulCount == 0 {
		return false, nil
	}
	rows, err := s.db.Query(ctx, "SELECT id, title, coalesce(court, ''), decided_on FROM ruling WHERE "+cond+
		" ORDER BY decided_on DESC NULLS LAST, id DESC LIMIT 10", args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var d DocRow
		if err := rows.Scan(&d.ID, &d.Title, &d.Kicker, &d.Date); err != nil {
			return false, err
		}
		d.Type = "ruling"
		res.Rulings = append(res.Rulings, d)
	}
	res.moreCited(true, cond, args)
	return true, rows.Err()
}

// titleStop are words no title search should be held up by.
var titleStop = map[string]bool{"في": true, "من": true, "على": true, "عن": true, "الى": true, "إلى": true, "او": true, "أو": true,
	"و": true, "ان": true, "أن": true, "هل": true, "ما": true, "لدى": true, "رقم": true, "تاريخ": true, "بتاريخ": true,
	"الصادر": true, "الصادرة": true, "المتعلق": true, "المتعلقة": true, "وتعديلاته": true, "وتعديلاتها": true, "تعديلاتها": true}

// titleKinds are kind words: they match the title or the kind, so "قانون
// تنظيم الملكية المشتركة" finds decree-law 88/1983, whose title has no «قانون».
var titleKinds = map[string]string{"قانون": "law", "القانون": "law", "مرسوم": "decree", "المرسوم": "decree",
	"قرار": "decision", "القرار": "decision", "نظام": "system", "النظام": "system",
	// plurals ("قوانين الجنسية"): the kind only, as no title says «قوانين»
	"قوانين": "laws", "القوانين": "laws", "مراسيم": "decrees", "المراسيم": "decrees", "قرارات": "decisions", "القرارات": "decisions"}

// pluralKinds are the kind groups a plural kind word stands for.
var pluralKinds = map[string]string{"laws": "law", "decrees": "decree", "decisions": "decision"}

// titleSynonyms are the words the texts use for what people type. Each was
// checked against real titles (21 Oct 2026): «القصار» → Law 422/2002 «حماية
// الاحداث…», «الجنسية» → Decision 15/1925 «التابعية اللبنانية».
// Keys are folded with arNormGo when the map is built.
var titleSynonyms = foldKeys(map[string][]string{
	"القصار": {"الاحداث", "القاصرين"}, "القاصرين": {"الاحداث", "القصار"}, "القصر": {"الاحداث", "القاصرين"},
	"الجنسية": {"التابعية"}, "جنسية": {"تابعية"}, "التابعية": {"الجنسية"},
})

func foldKeys(m map[string][]string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[arNormGo(k)] = v
	}
	return out
}

// titleConds turns query words into conditions on l.title, numbered from $start.
// A 1-2 digit word is the text's number, a year (1800-2100) may also be the
// issue year, a kind word may be the kind, and a word with synonyms matches any.
func titleConds(words []string, start int) ([]string, []any) {
	var conds []string
	var args []any
	next := func(a any) int { args = append(args, a); return start + len(args) - 1 }
	for i := 0; i < len(words); i++ {
		w := words[i]
		if titleStop[w] {
			continue
		}
		if n, ok := shortNumber(w); ok {
			conds = append(conds, fmt.Sprintf("l.number_int = $%d", next(n)))
			continue
		}
		if y, err := strconv.Atoi(w); err == nil && y >= 1800 && y <= 2100 {
			t := next("%" + w + "%")
			yn := next(y)
			conds = append(conds, fmt.Sprintf("(ar_norm(l.title) LIKE ar_norm($%d) OR (l.issued_on >= make_date($%d, 1, 1) AND l.issued_on < make_date($%d + 1, 1, 1)))", t, yn, yn))
			continue
		}
		// A longer number is the text's own number, or one its title cites
		// ("11244" is decree 11244/2003 first, then the decrees amending it).
		if n, err := strconv.Atoi(w); err == nil && n > 0 && len(w) <= 5 {
			nn := next(n)
			conds = append(conds, fmt.Sprintf("(l.number_int = $%d OR ar_norm(l.title) LIKE ar_norm($%d))", nn, next("%"+w+"%")))
			continue
		}
		if kind, ok := titleKinds[w]; ok {
			if one, plural := pluralKinds[kind]; plural {
				conds = append(conds, fmt.Sprintf("l.kind = ANY($%d)", next(kindGroups[one])))
				continue
			}
			if i+1 < len(words) && (strings.HasPrefix(arNormGo(words[i+1]), "اشتراع") || strings.HasPrefix(arNormGo(words[i+1]), "الاشتراع")) {
				kind, i = "decreelaw", i+1 // "مرسوم اشتراعي": the kind only
				conds = append(conds, fmt.Sprintf("l.kind = ANY($%d)", next(kindGroups[kind])))
				continue
			}
			t := next("%" + w + "%")
			conds = append(conds, fmt.Sprintf("(ar_norm(l.title) LIKE ar_norm($%d) OR l.kind = ANY($%d))", t, next(kindGroups[kind])))
			continue
		}
		alts := append([]string{w}, titleSynonyms[arNormGo(w)]...)
		var or []string
		for _, a := range alts {
			or = append(or, fmt.Sprintf("ar_norm(l.title) LIKE ar_norm($%d)", next("%"+a+"%")))
		}
		if len(or) == 1 {
			conds = append(conds, or[0])
		} else {
			conds = append(conds, "("+strings.Join(or, " OR ")+")")
		}
	}
	return conds, args
}
