package site

import (
	"html/template"
	"strings"
	"testing"
	"time"
)

func TestArticleRepealedByNoteRenders(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	note := "مادة ملغاة (الغيت بموجب 9640/1975 )"
	old := "نص قديم"
	from := time.Date(2017, 2, 16, 0, 0, 0, 0, time.UTC)
	law := &Law{LawRow: LawRow{ID: 190374, Kind: "قانون", Title: "قانون العمل", Status: "in_force"}}
	for _, a := range []*Article{
		{Number: "13", Note: &note},
		{Number: "29", Note: &note, Versions: []Version{
			{Major: 1, State: "in_force", Source: "none"},
			{Major: 2, From: &from, State: "in_force", TextAr: &old, Source: "amendment", Change: &Change{By: LawRow{ID: 1, Title: "x"}, ByArticle: "1"}},
		}},
	} {
		if !a.Repealed() || a.Shown() != "" {
			t.Errorf("%s: repealed %v, shown %q", a.Number, a.Repealed(), a.Shown())
		}
		for _, pages := range []map[string]*template.Template{s.pages, s.pagesAr} {
			var b strings.Builder
			if err := pages["law"].ExecuteTemplate(&b, "article", map[string]any{"Law": law, "A": a, "Standalone": true}); err != nil {
				t.Errorf("%s: %v", a.Number, err)
			}
		}
	}
}
