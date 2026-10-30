package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed en.json ar.json
var fs embed.FS

var English, Arabic map[string]string

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

type Bundle struct {
	Lang  string
	table map[string]string
}

func New(lang string) *Bundle {
	if lang == "ar" {
		return &Bundle{Lang: "ar", table: Arabic}
	}
	return &Bundle{Lang: "en", table: English}
}

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

var pluralNouns = []string{"article", "text", "opinion", "agreement", "study", "ruling", "issue"}

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
