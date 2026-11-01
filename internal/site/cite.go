package site

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	arDigits = strings.NewReplacer("٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9",
		"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9")
	citeDate  = regexp.MustCompile(`\b(\d{1,2})\s*[/.\-]\s*(\d{1,2})\s*[/.\-]\s*((?:19|20)\d{2})\b`)
	citeNumYr = regexp.MustCompile(`\b(\d{1,5})\s*/\s*((?:19|20)\d{2})\b`)
	citeBare  = regexp.MustCompile(`\b\d{1,5}\b`)
	// "4 aout 1967", "4 آب 1967": a day and a month word before a year.
	citeWordDate = regexp.MustCompile(`\b\d{1,2}\s+(?:janvier|fevrier|février|mars|avril|mai|juin|juillet|aout|août|septembre|octobre|novembre|decembre|décembre|january|february|march|april|may|june|july|august|september|october|november|december|كانون الثاني|كانون الاول|كانون الأول|شباط|اذار|آذار|نيسان|ايار|أيار|حزيران|تموز|اب|آب|ايلول|أيلول|تشرين الاول|تشرين الأول|تشرين الثاني)\s+((?:19|20)\d{2})\b`)
	citeNumber   = regexp.MustCompile(`(?:رقم|عدد|no\.?|n°|number)\s*(\d{1,5})\b`)
)

// parseCitation reads "decree 136 of 16/09/1983", "رقم 136 تاريخ 16/09/1983" or
// "513/2005" (number/year) from a query. ok is false when no number and year can be found.
func parseCitation(q string) (number string, year int, ok bool) {
	q = strings.ToLower(arDigits.Replace(q))
	if m := citeDate.FindStringSubmatch(q); m != nil {
		year, _ = strconv.Atoi(m[3])
		if n := citeNumber.FindStringSubmatch(strings.Replace(q, m[0], " ", 1)); n != nil {
			return n[1], year, true
		}
		// "قانون 337 تاريخ 8-6-1994": no "رقم", so take the one bare number left outside the date.
		rest := strings.Replace(q, m[0], " ", 1)
		if all := citeBare.FindAllString(rest, -1); len(all) == 1 {
			return all[0], year, true
		}
		return "", 0, false
	}
	if m := citeNumYr.FindStringSubmatch(q); m != nil {
		year, _ = strconv.Atoi(m[2])
		return m[1], year, true
	}
	// "decret-loi n.34 du 4 aout 1967": a date written with a month word.
	if m := citeWordDate.FindStringSubmatch(q); m != nil {
		year, _ = strconv.Atoi(m[1])
		rest := strings.Replace(q, m[0], " ", 1)
		if all := citeBare.FindAllString(rest, -1); len(all) == 1 {
			return all[0], year, true
		}
	}
	return "", 0, false
}

var (
	artWordNum = regexp.MustCompile(`(?:المادة|المادّة|مادة|الماده|ماده|article|art\.?)\s*(\d{1,4})\b`)
	artNumFrom = regexp.MustCompile(`^\s*(\d{1,4})\s+(?:من|of)\s+`)
	artNumLead = regexp.MustCompile(`^\s*(\d{1,4})\s+`)
	artStop    = map[string]bool{"من": true, "في": true, "قانون": true, "ال": true, "of": true, "the": true, "code": true, "law": true}
)

// parseArticleRef reads "المادة 248 موجبات وعقود" or "216 من قانون الملكية العقارية":
// the article number and the words naming the code. ok is false unless both are present.
func parseArticleRef(q string) (number string, words []string, ok bool) {
	q = strings.ToLower(arDigits.Replace(q))
	rest := ""
	if m := artWordNum.FindStringSubmatch(q); m != nil {
		number, rest = m[1], strings.Replace(q, m[0], " ", 1)
	} else if m := artNumFrom.FindStringSubmatch(q); m != nil {
		number, rest = m[1], strings.TrimPrefix(q, m[0])
	} else if m := artNumLead.FindStringSubmatch(q); m != nil {
		// "959 اصول محاكمات مدنيه": a bare number then a code name; the caller
		// only jumps when one principal code matches every word.
		number, rest = m[1], strings.TrimPrefix(q, m[0])
	} else {
		return "", nil, false
	}
	for _, w := range strings.Fields(rest) {
		if !artStop[w] {
			words = append(words, w)
		}
	}
	return number, words, len(words) > 0
}
