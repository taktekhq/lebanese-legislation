package site

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The unofficial English of a popular text's article shows on the English
// page, under the official Arabic, with its label; never on the Arabic page
// (Nizar, 5 Nov 2026: translate the popular laws with Gemini through agy).
func TestArticleEnglishTranslation(t *testing.T) {
	s, err := newServer(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	on := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	l := decree()
	a := &Article{Position: 1, Number: "1", TextAr: "ينشر هذا المرسوم ويعمل به فور نشره في الجريدة الرسمية.",
		En: "This decree shall be published and shall enter into force upon its publication in the Official Gazette.", EnOn: &on}
	l.List = []*Article{a}
	for lang, want := range map[string]bool{"en": true, "ar": false} {
		r := httptest.NewRequest("GET", "/decree/193806/article/1/", nil)
		r = r.WithContext(context.WithValue(r.Context(), langCtxKey, lang))
		p := s.articlePage(r, l, 0)
		p.Data.(map[string]any)["Topics"] = []TopicRef(nil)
		body, err := s.execute(r, "article", p)
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		has := strings.Contains(body, `<div class="art__en" lang="en" dir="ltr">`) && strings.Contains(body, "enter into force upon its publication")
		if has != want {
			t.Errorf("%s page: English shown %v, want %v", lang, has, want)
		}
		if want && !strings.Contains(body, "Unofficial English translation, made by machine (Gemini) on 05/11/2026") {
			t.Errorf("en page: no label")
		}
		if want && !strings.Contains(p.Desc, "This decree shall be published") {
			t.Errorf("en description does not use the English: %q", p.Desc)
		}
	}
	a.En = ""
	if a.English() != "" {
		t.Error("no translation, yet English() is not empty")
	}
}
