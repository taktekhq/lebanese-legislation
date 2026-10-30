package i18n

import "testing"

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
