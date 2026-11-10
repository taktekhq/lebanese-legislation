package site

import (
	"html/template"
	"strings"
	"testing"
	"time"
)

// An amendment the printed text does not include is said in the open: one
// line above the text naming the amending text, with or without links, and
// a head that does not date the text by that amendment as if it were in it.
func TestArticlePendingAmendmentIsVisible(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	law := &Law{LawRow: LawRow{ID: 243616, Kind: "مرسوم"}}
	day := time.Date(2016, 5, 26, 0, 0, 0, 0, time.UTC)
	change := &Change{Action: "replace", By: LawRow{ID: 264215, Kind: "مرسوم", Number: ptr("3416")}, ByArticle: "1",
		Effective: &day, TextAr: ptr("نص التعديل")}
	link := []Link{{Relation: "replace", By: change.By, ByArticle: "1", Text: "نص التعديل"}}
	pending := func(links []Link) *Article {
		return &Article{Number: "2", TextAr: "النص الحالي", Links: links, Versions: []Version{
			{Major: 1, State: "in_force", TextAr: ptr("النص الحالي"), Source: "centre"},
			{Major: 2, State: "in_force", From: &day, Change: change}}}
	}
	applied := &Article{Number: "2", Versions: []Version{
		{Major: 1, State: "in_force", TextAr: ptr("النص السابق"), Source: "centre"},
		{Major: 2, State: "in_force", From: &day, TextAr: ptr("النص الحالي"), Change: change, Source: "amendment"}}}
	langs := map[string]struct {
		pages         map[string]*template.Template
		line, pending string
		amended       string
	}{
		"en": {s.pages, "The text below may not include this change.", "Change of", "Amended"},
		"ar": {s.pagesAr, "قد لا يتضمن النص أدناه هذا التعديل.", "قد لا يكون في هذا النص", "معدلة"},
		"fr": {s.pagesFr, "Le texte ci-dessous n'intègre peut-être pas cette modification.", "peut-être non intégrée", "Modifié"},
	}
	render := func(pages map[string]*template.Template, a *Article) (open, head string) {
		var b strings.Builder
		if err := pages["law"].ExecuteTemplate(&b, "article", map[string]any{"Law": law, "A": a, "Standalone": true}); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		open = out
		if i := strings.Index(out, "<details class=\"art__amendments\""); i >= 0 {
			open = out[:i]
		}
		head = out[strings.Index(out, "art__tools"):strings.Index(out, "data-cite")]
		return open, head
	}
	for lang, l := range langs {
		for name, links := range map[string][]Link{"no links": nil, "links": link} {
			open, head := render(l.pages, pending(links))
			if !strings.Contains(open, l.line) || !strings.Contains(open, "/law/264215/article/1/") {
				t.Errorf("%s, %s: no visible line naming and linking the amending text", lang, name)
			}
			if strings.Contains(open, "نص التعديل") {
				t.Errorf("%s, %s: the amending wording is outside the disclosure", lang, name)
			}
			if !strings.Contains(head, l.pending) || strings.Contains(head, l.amended) {
				t.Errorf("%s, %s: head %q, want it to say the change is not in the text", lang, name, head)
			}
		}
		// An amendment the printed text includes: no such line, the usual head.
		open, head := render(l.pages, applied)
		if strings.Contains(open, l.line) || !strings.Contains(head, l.amended) || strings.Contains(head, l.pending) {
			t.Errorf("%s: applied amendment: head %q, line shown %v", lang, head, strings.Contains(open, l.line))
		}
	}
}
