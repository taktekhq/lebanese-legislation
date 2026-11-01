// Package i18n holds the site's user-interface strings in English and
// Arabic. The English file (en.json) is the source of truth; the Arabic
// file (ar.json) is a verbatim translation produced by Gemini (see
// lebaneselegislation's deploy notes) and is never hand-edited. A string
// missing from ar.json falls back to English and is reported by the tests.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed en.json ar.json
var fs embed.FS

// English and Arabic are the two loaded string tables, keyed the same way.
var English, Arabic map[string]string

// Missing lists the keys present in English but absent from Arabic, found
// at load time; the tests fail loudly if this is non-empty so a missing
// translation is never silently served.
var Missing []string

func init() {
	English = load("en.json")
	Arabic = load("ar.json")
	for k := range English {
		if _, ok := Arabic[k]; !ok {
			Missing = append(Missing, k)
		}
	}
}

func load(name string) map[string]string {
	b, err := fs.ReadFile(name)
	if err != nil {
		panic(err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		panic(fmt.Errorf("%s: %w", name, err))
	}
	return m
}

// Bundle looks strings up in one language, falling back to English.
type Bundle struct {
	Lang  string // "en" or "ar"
	table map[string]string
}

// New returns the bundle for "en" or "ar" (anything else behaves as "en").
func New(lang string) *Bundle {
	if lang == "ar" {
		return &Bundle{Lang: "ar", table: Arabic}
	}
	return &Bundle{Lang: "en", table: English}
}

// T looks up key and, if it holds any %s/%d etc., formats it with args.
// A key missing from the bundle's language falls back to English, then to
// the key itself so a typo is visible on the page instead of blank.
func (b *Bundle) T(key string, args ...any) string {
	s, ok := b.table[key]
	if !ok {
		s, ok = English[key]
		if !ok {
			return key
		}
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// pluralForms are the four Arabic numeral-agreement forms (one, two, few
// 3-10, many 11+) for the nouns the site counts: article, text, opinion,
// agreement, study, ruling, issue.
var pluralNouns = []string{"article", "text", "opinion", "agreement", "study", "ruling", "issue"}

// PluralAr renders a count the way Arabic agrees nouns with numbers, for
// one of the nouns above (matched by its English singular, e.g. "article"
// as already passed by the English templates). Falls back to the plain
// English pattern "%d word" if noun isn't one of the known ones.
func (b *Bundle) PluralAr(n int, enOne, enMany string) string {
	noun := enOne
	known := false
	for _, w := range pluralNouns {
		if w == noun {
			known = true
			break
		}
	}
	if !known {
		return fmt.Sprintf("%s %s", thousandsAr(n), pluralFallback(n, enOne, enMany))
	}
	switch {
	case n == 1:
		return b.T("plural." + noun + ".one")
	case n == 2:
		return b.T("plural." + noun + ".two")
	case mod100(n) >= 3 && mod100(n) <= 10:
		return thousandsAr(n) + " " + b.T("plural."+noun+".few")
	default:
		return thousandsAr(n) + " " + b.T("plural."+noun+".many")
	}
}

func mod100(n int) int {
	if n < 0 {
		n = -n
	}
	return n % 100
}

func pluralFallback(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func thousandsAr(n int) string {
	s := fmt.Sprint(n)
	neg := n < 0
	if neg {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
