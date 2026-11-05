package topics

import (
	"regexp"
	"strings"
)

// A cite may point at what we read ourselves from the Official Gazette's page
// images (gazette_annex, data/gazette_annex.sql) instead of an article of the
// Lebanese University's text: a row of a table annexed to the text,
// "annex-<category>-<serial>" (Law 243/2012's violation tables, category 1 to
// 5 or p for pedestrians), or an article's wording as first published,
// "original-<number>" (the Social Security Law's Articles 49 to 54 in 1963).
// The quote must then be an exact piece of that reading, the same check as
// for an article (Nizar, 5 Nov 2026: "just put them live").

// Reading is a cite's address in gazette_annex.
type Reading struct {
	Kind     string // "annex" or "original"
	Category string // annex: "1" to "5", "p"
	Serial   string // annex: the row's number in its table
	Number   string // original: the article's number
}

var (
	annexRe    = regexp.MustCompile(`^annex-([0-9]+|p)-([1-9][0-9]*)$`)
	originalRe = regexp.MustCompile(`^original-([0-9]+(?:-[0-9]+)?)$`)
)

// ParseReading reads a Cite.Article as a Gazette reading; ok is false for
// an ordinary article number.
func ParseReading(article string) (r Reading, ok bool) {
	if m := annexRe.FindStringSubmatch(article); m != nil {
		return Reading{Kind: "annex", Category: m[1], Serial: m[2]}, true
	}
	if m := originalRe.FindStringSubmatch(article); m != nil {
		return Reading{Kind: "original", Number: m[1]}, true
	}
	return Reading{}, false
}

// isReadingLike says whether an article value is meant as a reading, so a
// malformed one ("annex-3", "original-") is reported rather than looked up
// as an article that does not exist.
func isReadingLike(article string) bool {
	return strings.HasPrefix(article, "annex-") || strings.HasPrefix(article, "original-")
}

// decimalComma is a reading's text with its decimal commas as points: the
// tables print "٠,٥ غ/ل" for half a gram, which a statement writes 0.5.
var decimalComma = regexp.MustCompile(`([0-9]+),([0-9]{1,2})(?:[^0-9]|$)`)

func decimals(s string) string {
	s = digitsASCII.Replace(s) // Arabic digits and commas to ASCII first

	var out []string
	for _, m := range decimalComma.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1]+"."+m[2])
	}
	return strings.Join(out, " ")
}
