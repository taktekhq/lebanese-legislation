package snapshot

import (
	"regexp"
	"strings"
)

// arNorm is the Go twin of the Postgres ar_norm SQL function (db/schema.sql):
// it drops tashkeel and tatweel, then folds أ إ آ ٱ to ا, ى to ي and ة to ه,
// so one spelling of a word finds every other. Used only to precompute a
// search column at export time; the stored text_ar/title stay as printed.
//
// Postgres: regexp_replace(t, '[ً-ٰٟـ]', ”, 'g') then
// translate(..., 'أإآٱىة', 'اااايه').
var arDiacritics = regexp.MustCompile(`[\x{064B}-\x{065F}\x{0670}\x{0640}]`)

var arFold = map[rune]rune{
	'أ': 'ا', 'إ': 'ا', 'آ': 'ا', 'ٱ': 'ا', 'ى': 'ي', 'ة': 'ه',
}

func arNorm(s string) string {
	if s == "" {
		return s
	}
	s = arDiacritics.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if f, ok := arFold[r]; ok {
			b.WriteRune(f)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
