package site

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// When a search finds nothing, these are tried in turn, cheapest first, and
// the first that finds something is shown with a line saying what was
// searched instead. None of them writes anything shown as law; they only
// mend the question.
//
//	translated  Latin words to Arabic: the glossary (gloss.go), then names and
//	            Arabizi read letter by letter against the title words (translit.go)
//	adjusted    a number or a year in the question narrows the texts
//	            ("513 هيئة إدارة السير", "... 2024"), or a typo is mended
//	            ("الاستلذ")
//
// Setting the commonest words aside is no longer a reading of its own: the
// search does it in the same query (internal/wordsearch), and the titles
// that hold all but one word are the close titles.

var (
	searchYear = regexp.MustCompile(`^(?:19|20)\d\d$`)
	searchNum  = regexp.MustCompile(`^\d{1,5}$`)
	// little Arabic words that no title search should be held up by
	searchStop = map[string]bool{"في": true, "من": true, "على": true, "عن": true, "الى": true, "إلى": true, "او": true, "أو": true,
		"و": true, "ان": true, "أن": true, "هل": true, "ما": true, "رقم": true, "تاريخ": true}
)

// splitNumbers pulls a text number and a year out of a query, leaving the words.
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

// rescue returns the results of the first way of reading the query that
// finds anything, and how ("" when none did).
func (s *Server) rescue(ctx context.Context, q string) (Results, string, error) {
	v := s.vocab.Load()
	num, year, words := splitNumbers(q)
	kind := "adjusted"
	if !hasArabic(q) { // English, French, a name or Arabizi: read it into Arabic words first
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
		if num != "" || year != 0 { // say the number or the year too
			alt.Via = strings.Join(strings.Fields(alt.Q+" "+num+" "+yearStr(year)), " ")
		}
		alt.Q = q
		return alt, how, found(alt), nil
	}
	// The question as read, with its number and year.
	if res, how, ok, err := try(words, num, year, kind, "read"); err != nil || ok {
		return res, how, err
	}
	// Without the number and the year ("513" may be an article, not a decree).
	if num != "" || year != 0 {
		if res, how, ok, err := try(words, "", 0, "adjusted", "words"); err != nil || ok {
			return res, how, err
		}
	}
	// A typo: a word no title holds is replaced by the nearest one that some title does.
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
	return Results{}, "", nil
}

func yearStr(y int) string {
	if y == 0 {
		return ""
	}
	return strconv.Itoa(y)
}

// dropOne leaves out the word that tells least: one no title holds, else the
// commonest. The rest keep their order.
func dropOne(v *vocab, words []string) []string {
	drop, best := 0, -1
	for i, w := range words {
		n := wordWeight(v, w)
		if n == 0 {
			n = 1 << 30 // unknown: first to go
		}
		if n > best {
			drop, best = i, n
		}
	}
	out := make([]string, 0, len(words)-1)
	out = append(out, words[:drop]...)
	return append(out, words[drop+1:]...)
}

// wordWeight is how often a word occurs in titles; unknown words weigh 0.
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

// closeTitles are the titles that hold all but one of the query's terms,
// for a search whose words no title holds together but some articles do:
// "قانون الغابات اللبناني" listed 56 articles and not the forest law, whose
// title has no «اللبناني» (22 Oct 2026). The term that tells least goes (the
// commonest, or one no title holds: internal/wordsearch keeps the rarest).
// One word left alone is not enough ("عدم الصفة" is not every title with
// «الصفة»), unless a kind word came with it; two dropped words drifted
// ("المرأة اللبنانية المقترنة بأجنبي" became any title on Lebanese women).
func (s *Server) closeTitles(ctx context.Context, q string) []LawRow {
	if !hasArabic(q) {
		return nil
	}
	var kinds, words int
	for _, w := range strings.Fields(q) {
		switch {
		case titleStop[w]:
		case titleKinds[w] != "":
			kinds++
		default:
			words++
		}
	}
	if words < 2 || (words == 2 && kinds == 0) {
		return nil
	}
	terms := titleTerms(strings.Fields(q))
	_, hits, err := s.rankedLaws(ctx, q, 0, "", len(terms)-1, 5, nil)
	if err != nil {
		return nil
	}
	return lawRowsOf(hits)
}

// queryNumbers are the numbers in a query that may be a text's number: not a
// year, at most five digits.
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

// articleQuery is the query an article search runs: without its kind words
// and little words when at least two others remain. "قانون المرأة اللبنانية
// المقترنة بأجنبي" found no article, as none says «قانون» too (22 Oct 2026);
// the kind word still counts for titles.
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

// nearTitles are the titles most like the query, for a page that found
// nothing: something to click instead of an empty page.
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

// sameNumber lists the texts that carry a number, whatever their date: shown
// under "no text has that number and date", so a wrong year is one click away
// (decree 9341 asked as 2012 is 2022). Never a silent fallback.
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
