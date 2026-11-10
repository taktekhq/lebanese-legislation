package i18n

import (
	"regexp"
	"strings"
	"testing"
)

// TestEveryKeyHasArabic logs (never fails the build) any UI string that
// has no Arabic translation yet, so a gap is visible without blocking
// deploys; a missing key falls back to English on the page itself.
func TestEveryKeyHasArabic(t *testing.T) {
	if len(Missing) > 0 {
		t.Logf("%d UI string(s) have no Arabic translation yet, falling back to English: %v", len(Missing), Missing)
	}
}

func TestLookupFallsBackToEnglish(t *testing.T) {
	b := New("ar")
	if got := b.T("brand.name"); got == "" || got == "brand.name" {
		t.Fatalf("brand.name: got %q", got)
	}
	if got := New("en").T("nonexistent.key.xyz"); got != "nonexistent.key.xyz" {
		t.Fatalf("unknown key should fall back to the key itself, got %q", got)
	}
}

func TestPluralAr(t *testing.T) {
	b := New("ar")
	cases := []struct {
		n    int
		want string
	}{
		{1, "مادة واحدة"},
		{2, "مادتان"},
		{3, "3 مواد"},
		{10, "10 مواد"},
		{11, "11 مادة"},
		{100, "100 مادة"},
	}
	for _, c := range cases {
		if got := b.PluralAr(c.n, "article", "articles"); got != c.want {
			t.Errorf("PluralAr(%d, article): got %q, want %q", c.n, got, c.want)
		}
	}
}

// TestEveryKeyHasFrench logs (never fails the build) any UI string that has
// no French translation yet, as for Arabic: it falls back to English.
func TestEveryKeyHasFrench(t *testing.T) {
	if len(MissingFrench) > 0 {
		t.Logf("%d UI string(s) have no French translation yet, falling back to English: %v", len(MissingFrench), MissingFrench)
	}
}

// A French string formats the same arguments as its English one, and holds
// no em or en dash.
func TestFrenchStringsMatchEnglish(t *testing.T) {
	verbs := regexp.MustCompile(`%%|%[sd]`)
	for k, fr := range French {
		en, ok := English[k]
		if !ok {
			continue // a string of a page that is not merged yet
		}
		a, b := verbs.FindAllString(en, -1), verbs.FindAllString(fr, -1)
		if strings.Join(a, " ") != strings.Join(b, " ") {
			t.Errorf("%s: French formats %v, English %v", k, b, a)
		}
		if strings.ContainsAny(fr, "\u2014\u2013") {
			t.Errorf("%s: French holds a dash", k)
		}
	}
}

func TestPluralFr(t *testing.T) {
	b := New("fr")
	for n, want := range map[int]string{0: "0 article", 1: "1 article", 2: "2 articles", 12345: "12\u202f345 articles"} {
		if got := b.PluralFr(n, "article", "articles"); got != want {
			t.Errorf("PluralFr(%d): got %q, want %q", n, got, want)
		}
	}
	if got := b.PluralFr(3, "ruling", "rulings"); got != "3 décisions" {
		t.Errorf("PluralFr(3, ruling): got %q", got)
	}
}
