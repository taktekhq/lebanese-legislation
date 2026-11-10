// Package i18n holds the site's user-interface strings in English, Arabic
// and French. The English file (en.json) is the source of truth; the Arabic
// file (ar.json) is a verbatim translation produced by Gemini (see
// lebaneselegislation's deploy notes) and is never hand-edited. The French
// file (fr.json) translates the interface only: law texts and titles stay as
// they are. A string missing from ar.json or fr.json falls back to English
// and is reported by the tests.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed en.json ar.json fr.json
var fs embed.FS

// English, Arabic and French are the loaded string tables, keyed the same way.
var English, Arabic, French map[string]string

// Missing lists the keys present in English but absent from Arabic, found
// at load time; the tests fail loudly if this is non-empty so a missing
// translation is never silently served.
var Missing []string

// MissingFrench lists the keys present in English but absent from French.
var MissingFrench []string

func init() {
	English = load("en.json")
	Arabic = load("ar.json")
	French = load("fr.json")
	for k := range English {
		if _, ok := Arabic[k]; !ok {
			Missing = append(Missing, k)
		}
		if _, ok := French[k]; !ok {
			MissingFrench = append(MissingFrench, k)
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
	Lang  string // "en", "ar" or "fr"
	table map[string]string
	note  func(key string)
}

// Noting returns a copy of b that tells note every key it looks up (the
// static export records which strings a page used).
func (b *Bundle) Noting(note func(key string)) *Bundle {
	c := *b
	c.note = note
	return &c
}

// New returns the bundle for "en", "ar" or "fr" (anything else behaves as "en").
func New(lang string) *Bundle {
	switch lang {
	case "ar":
		return &Bundle{Lang: "ar", table: Arabic}
	case "fr":
		return &Bundle{Lang: "fr", table: French}
	}
	return &Bundle{Lang: "en", table: English}
}

// T looks up key and, if it holds any %s/%d etc., formats it with args.
// A key missing from the bundle's language falls back to English, then to
// the key itself so a typo is visible on the page instead of blank.
func (b *Bundle) T(key string, args ...any) string {
	if b.note != nil {
		b.note(key)
	}
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

// PluralFr renders a count with its French noun for the nouns the site
// counts (plural.<noun>.one and .many in fr.json), matched by the English
// singular the templates pass. French uses the singular for 0 and 1, and a
// narrow no-break space between thousands. An unknown noun keeps its English
// words.
func (b *Bundle) PluralFr(n int, enOne, enMany string) string {
	one, many := enOne, enMany
	for _, w := range pluralNouns {
		if w == enOne {
			one, many = b.T("plural."+w+".one"), b.T("plural."+w+".many")
		}
	}
	if n <= 1 && n >= -1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return ThousandsFr(n) + " " + many
}

// ThousandsFr prints n the French way: 12 345, with narrow no-break spaces.
func ThousandsFr(n int) string {
	s := fmt.Sprint(n)
	neg := n < 0
	if neg {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "\u202f" + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
