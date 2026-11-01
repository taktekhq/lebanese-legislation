package site

import (
	"encoding/json"
	"html/template"
	"os"
	"strings"
	"testing"
)

func TestCivilProcedureAmounts(t *testing.T) {
	cases := []struct{ article, want string }{
		{"86", "5,000,000,000 (خمسة مليارات) ليرة"},
		{"127", "10,000,000 (عشرة ملايين) و40,000,000 (أربعين مليون) ليرة"},
	}
	// Full source fixtures exercise the pinned source and every monetary article.
	sources := civilProcedureFixture(t)
	law := &Law{LawRow: LawRow{ID: 244565}}
	for _, c := range cases {
		a := &Article{Number: c.article, TextAr: sources[c.article]}
		got := law.ArticleAmounts(a)
		if got == nil || !strings.Contains(got.Text, c.want) {
			t.Fatalf("article %s: %+v, want %q", c.article, got, c.want)
		}
		if a.TextAr != sources[c.article] || a.Shown() != sources[c.article] {
			t.Fatal("source wording mutated")
		}
		if second := law.ArticleAmounts(a); second.Text != got.Text {
			t.Fatal("calculation applied twice")
		}
		for _, other := range []int{244611, 246455, 330819, 190374} {
			if (&Law{LawRow: LawRow{ID: other}}).ArticleAmounts(a) != nil {
				t.Fatalf("50x escaped Civil Procedure: law %d", other)
			}
		}
		changed := *a
		changed.TextAr += " تعديل لاحق"
		if law.ArticleAmounts(&changed) != nil {
			t.Fatal("unreviewed or later wording was multiplied")
		}
	}
}

func TestCivilProcedureAmountsRender(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	law := &Law{LawRow: LawRow{ID: 244565}}
	a := &Article{Number: "86", TextAr: civilProcedureFixture(t)["86"]}
	for lang, pages := range map[string]map[string]*template.Template{"en": s.pages, "ar": s.pagesAr} {
		var b strings.Builder
		if err := pages["law"].ExecuteTemplate(&b, "article", map[string]any{"Law": law, "A": a, "Standalone": true}); err != nil {
			t.Fatal(err)
		}
		label := "Monetary values updated."
		if lang == "ar" {
			label = "تم تحديث المبالغ المالية."
		}
		for _, want := range []string{"5,000,000,000", "13909/2024", "/330819/article/1/", "ماية مليون", "<strong>" + label + "</strong>"} {
			if !strings.Contains(b.String(), want) {
				t.Errorf("render lacks %q", want)
			}
		}
	}
}

func civilProcedureFixture(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("testdata/civil-procedure-amounts.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Number string `json:"number"`
		Text   string `json:"text_ar"`
	}
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.Number] = r.Text
	}
	return out
}

func TestCivilProcedureAllMonetaryArticles(t *testing.T) {
	law := &Law{LawRow: LawRow{ID: 244565}}
	sources := civilProcedureFixture(t)
	adjusted := 0
	for number, source := range sources {
		a := &Article{Number: number, TextAr: source}
		got := law.ArticleAmounts(a)
		if number == "420" || number == "904" || number == "918" {
			if got != nil {
				t.Fatalf("nonmonetary article %s adjusted", number)
			}
			continue
		}
		if got == nil || got.Text == source {
			t.Fatalf("monetary article %s not adjusted", number)
		}
		adjusted++
	}
	if adjusted != 34 {
		t.Fatalf("adjusted %d monetary articles, want 34", adjusted)
	}
	// Article 928 includes a monetary threshold and a nonmonetary percentage.
	got := law.ArticleAmounts(&Article{Number: "928", TextAr: sources["928"]})
	if !strings.Contains(got.Text, "8,000,000 (ثمانية ملايين) ليرة") || !strings.Contains(got.Text, "ستين بالمائة") {
		t.Fatalf("threshold/percentage: %s", got.Text)
	}
	// Range endpoints must both change even when only the end names the currency.
	got = law.ArticleAmounts(&Article{Number: "189", TextAr: sources["189"]})
	if !strings.Contains(got.Text, "من 1,000,000 (مليون) الى 10,000,000 (عشرة ملايين) ليرة") {
		t.Fatalf("range: %s", got.Text)
	}
	// Already calculated or newer version text must never receive another 50x.
	a := &Article{Number: "86", TextAr: sources["86"]}
	calculated := law.ArticleAmounts(a).Text
	a.Versions = []Version{{Major: 2, State: "in_force", TextAr: &calculated}}
	if law.ArticleAmounts(a) != nil {
		t.Fatal("already calculated version multiplied twice")
	}
	a = &Article{Number: "86", TextAr: sources["86"], Versions: []Version{{Major: 2, State: "repealed"}}}
	if law.ArticleAmounts(a) != nil {
		t.Fatal("repealed article adjusted")
	}
}

func TestAmountBannerOnlyForChangedValues(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	source := civilProcedureFixture(t)["86"]
	for _, c := range []struct {
		law  int
		text string
	}{
		{244611, source}, {244565, source + " تعديل لاحق"}, {244565, "نص بلا مبالغ"},
	} {
		law := &Law{LawRow: LawRow{ID: c.law}}
		a := &Article{Number: "86", TextAr: c.text}
		var b strings.Builder
		if err := s.pages["law"].ExecuteTemplate(&b, "article", map[string]any{"Law": law, "A": a, "Standalone": true}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(b.String(), "Monetary values updated.") || strings.Contains(b.String(), "data-adjusted-amounts") {
			t.Fatalf("false update notice on law %d: %s", c.law, c.text)
		}
	}
}

func TestCivilProcedureNumericAndArabicAmounts(t *testing.T) {
	sources := civilProcedureFixture(t)
	law := &Law{LawRow: LawRow{ID: 244565}}
	cases := []struct {
		article string
		want    []string
	}{
		{"11", []string{"2,000,000 (مليونا) ليرة", "100,000,000 (مائة مليون) ليرة"}},
		{"117", []string{"2,000,000 (مليونا) ليرة"}},
		{"189", []string{"1,000,000 (مليون)", "10,000,000 (عشرة ملايين) ليرة"}},
		{"279", []string{"500,000 (خمسمائة ألف)", "5,000,000 (خمسة ملايين) ليرة"}},
		{"928", []string{"8,000,000 (ثمانية ملايين) ليرة"}},
		{"127", []string{"10,000,000 (عشرة ملايين)", "40,000,000 (أربعين مليون) ليرة"}},
		{"748", []string{"20,000,000 (عشرين مليون) ليرة", "40,000,000 (أربعين مليون) ليرة"}},
		{"254", []string{"25,000,000 (خمسة وعشرين مليون) ليرة"}},
		{"378", []string{"50,000,000 (خمسين مليون) ليرة"}},
		{"640", []string{"150,000,000 (مائة وخمسين مليون) ليرة"}},
		{"709", []string{"300,000,000 (ثلاثمائة مليون) ليرة"}},
		{"86", []string{"5,000,000,000 (خمسة مليارات) ليرة"}},
		{"656", []string{"2,000,000 (مليوني) ليرة"}},
	}
	for _, c := range cases {
		got := law.ArticleAmounts(&Article{Number: c.article, TextAr: sources[c.article]})
		if got == nil {
			t.Fatalf("article %s has no adjustment", c.article)
		}
		for _, want := range c.want {
			if !strings.Contains(got.Text, want) {
				t.Errorf("article %s lacks %q: %s", c.article, want, got.Text)
			}
		}
	}
}
