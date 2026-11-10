package site

import (
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

// The topic checker (topics.Shown) and the article page (Article.Shown) pick
// the same wording: a quote the checker accepts is on the page it links to.
func TestTopicCheckerReadsWhatTheArticlePagePrints(t *testing.T) {
	const centre = "النص الكامل للمادة كما يحفظه المركز"
	clause, old, cur := "البند السادس فقرة (4) (ب) (الجديدة)", "النص السابق", "النص الحالي"
	amended, repealed := "(عدلت بموجب قانون 323/2023 )", " مادة ملغاة (الغيت بموجب قانون 1/2000 )"
	cases := map[string]*Article{
		"never amended":       {Number: "1", TextAr: centre},
		"no wording":          {Number: "1", TextAr: centre, Note: &amended, Versions: []Version{{Major: 1, State: "in_force"}, {Major: 2, State: "in_force"}}},
		"latest wording":      {Number: "1", TextAr: centre, Versions: []Version{{Major: 1, State: "in_force", TextAr: &old}, {Major: 2, State: "in_force", TextAr: &cur}}},
		"correction":          {Number: "1", TextAr: centre, Versions: []Version{{Major: 1, State: "in_force", TextAr: &old}, {Major: 1, Minor: 1, State: "in_force", TextAr: &cur}}},
		"repealed by version": {Number: "1", TextAr: centre, Versions: []Version{{Major: 1, State: "in_force", TextAr: &old}, {Major: 2, State: "repealed"}}},
		"repealed by note":    {Number: "1", TextAr: centre, Note: &repealed, Versions: []Version{{Major: 1, State: "in_force", TextAr: &old}}},
		// Social Security Law Article 9 on 10 Nov 2026 (see topics.TestShown).
		"SSL Article 9": {Number: "9", TextAr: centre, Note: &amended, Versions: []Version{{Major: 1, State: "in_force"},
			{Major: 2, State: "in_force"}, {Major: 3, State: "in_force", TextAr: &clause, Source: "amendment"}, {Major: 4, State: "in_force"}}},
	}
	for name, a := range cases {
		note := ""
		if a.Note != nil {
			note = *a.Note
		}
		var versions []*string
		for _, v := range a.Versions {
			versions = append(versions, v.TextAr)
		}
		if got, want := topics.Shown(a.TextAr, note, versions), a.Shown(); got != want {
			t.Errorf("%s: the checker reads %q, the page prints %q", name, got, want)
		}
	}
	if got := cases["SSL Article 9"].Shown(); got != clause {
		t.Errorf("SSL Article 9: the page prints %q, want the clause", got)
	}
}
