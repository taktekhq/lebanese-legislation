package site

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func (s *Server) findLawCite(ctx context.Context, res *Results, r queryRead) (bool, error) {
	type tier struct {
		cond string
		args []any
		off  bool
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

			tier{cond: "l.number_int = $1 AND extract(month FROM l.issued_on) = $2 AND extract(day FROM l.issued_on) = $3",
				args: []any{r.Num, int(r.Date.Month()), r.Date.Day()}, off: true})
	case r.Num > 0 && r.Year > 0:
		tiers = append(tiers, tier{cond: "l.number_int = $1 AND " + yearCond(2), args: []any{r.Num, r.Year}})
	case r.Num > 0:
		tiers = append(tiers, tier{cond: "l.number_int = $1", args: []any{r.Num}})
	}

	if r.Num > 0 && r.Year > 0 {
		for _, w := range r.Words {
			if n, err := strconv.Atoi(w); err == nil && n != r.Num && len(w) >= 3 && len(w) <= 5 {
				tiers = append(tiers, tier{cond: "l.number_int = $1 AND ar_norm(l.title) LIKE ar_norm($2)", args: []any{r.Num, "%" + w + "%"}, off: true})
			}
		}
	}
	if !r.Date.IsZero() && r.Num == 0 {
		if len(r.Words) > 0 {
			var or []string
			args := []any{date}
			for _, w := range r.Words {
				args = append(args, "%"+w+"%")
				or = append(or, fmt.Sprintf("ar_norm(l.title) LIKE ar_norm($%d)", len(args)))
			}
			tiers = append(tiers, tier{cond: "l.issued_on = $1::date AND (" + strings.Join(or, " OR ") + ")", args: args})

			conds, wargs := titleConds(r.Words, 1)
			if len(conds) > 0 {
				wargs = append(wargs, r.Year)
				tiers = append(tiers, tier{cond: strings.Join(conds, " AND ") + " AND " + yearCond(len(wargs)), args: wargs})
			}

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

		if alt := s.closeTitleOtherYear(ctx, r, res.Laws); alt != nil {
			var added bool
			if res.Laws, added = withPin(res.Laws, alt, 10); added {
				res.LawCount++
			}
			t.off = true
		}
		if t.off && len(res.Laws) > 0 {
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
	return true, rows.Err()
}

var titleStop = map[string]bool{"في": true, "من": true, "على": true, "عن": true, "الى": true, "إلى": true, "او": true, "أو": true,
	"و": true, "ان": true, "أن": true, "هل": true, "ما": true, "لدى": true, "رقم": true, "تاريخ": true, "بتاريخ": true,
	"الصادر": true, "الصادرة": true, "المتعلق": true, "المتعلقة": true, "وتعديلاته": true, "وتعديلاتها": true, "تعديلاتها": true}

var titleKinds = map[string]string{"قانون": "law", "القانون": "law", "مرسوم": "decree", "المرسوم": "decree",
	"قرار": "decision", "القرار": "decision", "نظام": "system", "النظام": "system",

	"قوانين": "laws", "القوانين": "laws", "مراسيم": "decrees", "المراسيم": "decrees", "قرارات": "decisions", "القرارات": "decisions"}

var pluralKinds = map[string]string{"laws": "law", "decrees": "decree", "decisions": "decision"}

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
				kind, i = "decreelaw", i+1
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
