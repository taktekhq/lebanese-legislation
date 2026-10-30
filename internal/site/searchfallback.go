package site

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

var (
	searchYear = regexp.MustCompile(`^(?:19|20)\d\d$`)
	searchNum  = regexp.MustCompile(`^\d{1,5}$`)

	searchStop = map[string]bool{"في": true, "من": true, "على": true, "عن": true, "الى": true, "إلى": true, "او": true, "أو": true,
		"و": true, "ان": true, "أن": true, "هل": true, "ما": true, "رقم": true, "تاريخ": true}
)

func splitNumbers(q string) (num string, year int, words []string) {
	for _, w := range strings.Fields(arDigits.Replace(q)) {
		switch {
		case searchYear.MatchString(w) && year == 0:
			year, _ = strconv.Atoi(w)
		case searchNum.MatchString(w) && num == "":
			num = w
		case searchStop[w]:
		default:
			words = append(words, w)
		}
	}
	return
}

func found(r Results) bool { return r.LawCount+r.ArtCount+r.RulCount+len(r.Topics) > 0 }

func (s *Server) rescue(ctx context.Context, q string) (Results, string, error) {
	v := s.vocab.Load()
	num, year, words := splitNumbers(q)
	kind := "adjusted"
	if !hasArabic(q) {
		plain := strings.Join(words, " ")
		ar, whole := glossQueryWhole(plain)
		if !whole {
			ar = latinWords(plain, v)
		}
		if ar == "" {
			ar = glossQuery(plain)
		}
		if ar == "" && num == "" && year == 0 {
			return Results{}, "", nil
		}
		words = strings.Fields(ar)
		if num == "" && year == 0 {
			kind = "translated"
		}
	}
	tried := map[string]bool{}
	try := func(words []string, num string, year int, kind, how string) (Results, string, bool, error) {
		alt := Results{Q: strings.Join(words, " "), Num: num, Year: year}
		key := alt.Q + "|" + num + "|" + strconv.Itoa(year)
		if (alt.Q == "" && num == "" && year == 0) || tried[key] {
			return alt, "", false, nil
		}
		tried[key] = true
		if err := s.find(ctx, &alt); err != nil {
			return alt, "", false, err
		}
		alt.Via, alt.ViaKind = alt.Q, kind
		if num != "" || year != 0 {
			alt.Via = strings.Join(strings.Fields(alt.Q+" "+num+" "+yearStr(year)), " ")
		}
		alt.Q = q
		return alt, how, found(alt), nil
	}

	if res, how, ok, err := try(words, num, year, kind, "read"); err != nil || ok {
		return res, how, err
	}

	if year != 0 && num == "" {
		rest := words
		for i := 0; i < 2 && len(rest) > 1; i++ {
			rest = dropOne(v, rest)
			if res, how, ok, err := try(rest, "", year, "adjusted", "year"); err != nil || ok {
				return res, how, err
			}
		}
	}

	if num != "" || year != 0 {
		if res, how, ok, err := try(words, "", 0, "adjusted", "words"); err != nil || ok {
			return res, how, err
		}
	}

	fixed := make([]string, 0, len(words))
	changed := false
	for _, w := range words {
		w = arNormGo(w)
		if isArabicWord(w) && len([]rune(w)) >= 4 && v != nil && !v.has(w) && !v.has(strings.TrimPrefix(w, "ال")) {
			if n, ok := v.nearest(w); ok {
				w, changed = n, true
			}
		}
		fixed = append(fixed, w)
	}
	if changed {
		if res, how, ok, err := try(fixed, "", 0, "adjusted", "spell"); err != nil || ok {
			return res, how, err
		}
	}

	rest := fixed
	for i := 0; i < 3 && len(rest) > 1; i++ {
		rest = dropOne(v, rest)
		if len(rest) == 1 && wordWeight(v, rest[0]) > 500 {
			break
		}
		if res, how, ok, err := try(rest, "", 0, "adjusted", "relax"); err != nil || ok {
			return res, how, err
		}
	}
	return Results{}, "", nil
}

func yearStr(y int) string {
	if y == 0 {
		return ""
	}
	return strconv.Itoa(y)
}

func dropOne(v *vocab, words []string) []string {
	drop, best := 0, -1
	for i, w := range words {
		n := wordWeight(v, w)
		if n == 0 {
			n = 1 << 30
		}
		if n > best {
			drop, best = i, n
		}
	}
	out := make([]string, 0, len(words)-1)
	out = append(out, words[:drop]...)
	return append(out, words[drop+1:]...)
}

func wordWeight(v *vocab, w string) int {
	if v == nil {
		return 0
	}
	w = arNormGo(w)
	if n := v.freq[w]; n > 0 {
		return n
	}
	return v.freq[strings.TrimPrefix(w, "ال")]
}

func (s *Server) closeTitles(ctx context.Context, q string) []LawRow {
	v := s.vocab.Load()
	if v == nil || !hasArabic(q) {
		return nil
	}
	var kinds, words []string
	for _, w := range strings.Fields(q) {
		switch {
		case titleStop[w]:
		case titleKinds[w] != "":
			kinds = append(kinds, w)
		default:
			words = append(words, w)
		}
	}
	if len(words) < 2 {
		return nil
	}
	words = dropOne(v, words)
	if len(words) == 1 && (len(kinds) == 0 || wordWeight(v, words[0]) > 500) {
		return nil
	}
	alt := Results{Q: strings.Join(append(append([]string{}, kinds...), words...), " "), rank: q}
	if err := s.findLaws(ctx, &alt); err != nil || alt.LawCount == 0 {
		return nil
	}
	return alt.Laws[:min(5, len(alt.Laws))]
}

func queryNumbers(q string) []int {
	var out []int
	for _, w := range strings.Fields(q) {
		if !searchNum.MatchString(w) || searchYear.MatchString(w) {
			continue
		}
		if n, _ := strconv.Atoi(w); n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func articleQuery(q string) string {
	ws := strings.Fields(q)
	keep := make([]string, 0, len(ws))
	for _, w := range ws {
		if titleKinds[w] != "" || titleStop[w] {
			continue
		}
		keep = append(keep, w)
	}
	if len(keep) >= 2 && len(keep) < len(ws) {
		return strings.Join(keep, " ")
	}
	return q
}

func (s *Server) nearTitles(ctx context.Context, q string) []LawRow {
	if !hasArabic(q) || len([]rune(q)) < 3 {
		return nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE ar_norm($1) <% ar_norm(l.title)
		ORDER BY word_similarity(ar_norm($1), ar_norm(l.title)) DESC, (EXISTS (SELECT 1 FROM code c WHERE c.law_id = l.id)) DESC LIMIT 5`, q)
	if err != nil {
		return nil
	}
	near, err := scanLawRows(rows)
	if err != nil {
		return nil
	}
	return near
}

func (s *Server) sameNumber(ctx context.Context, n int) []LawRow {
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE l.number_int = $1 ORDER BY l.issued_on DESC NULLS LAST LIMIT 8`, n)
	if err != nil {
		return nil
	}
	out, err := scanLawRows(rows)
	if err != nil {
		return nil
	}
	return out
}
