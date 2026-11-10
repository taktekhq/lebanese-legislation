package site

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
)

// The language bar sits between the header and the content on every page:
// the three languages in one order, the page's own marked as current and not
// a link, the two others linked. The header holds no language link.
func TestLanguageBar(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	item := regexp.MustCompile(`<li>(<a class="langbar__link lang-switch"[^>]*hreflang="(\w+)"[^>]*>|<span class="langbar__current" aria-current="true" lang="(\w+)">)([^<]+)</`)
	for lang, path := range map[string]string{"en": "/en/contribute/", "ar": "/contribute/", "fr": "/fr/contribute/"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		body := rec.Body.String()
		header, bar, main := strings.Index(body, `</header>`), strings.Index(body, `<nav class="langbar" aria-label="`+i18n.New(lang).T("langbar.label")+`">`), strings.Index(body, `<main id="top">`)
		if header < 0 || bar < header || main < bar {
			t.Fatalf("%s: the language bar is not between the header and the content, with its translated label", path)
		}
		if strings.Contains(body[:header], "lang-switch") {
			t.Errorf("%s: the header still holds a language link", path)
		}
		if !strings.Contains(body[:header], `class="btn btn--primary btn--sm" href="`) {
			t.Errorf("%s: the header lost its button", path)
		}
		got := item.FindAllStringSubmatch(body[bar:main], -1)
		if len(got) != 3 {
			t.Fatalf("%s: %d language options, want 3", path, len(got))
		}
		for i, want := range []struct{ code, name string }{{"en", "English"}, {"ar", "العربية"}, {"fr", "Français"}} {
			code, current := got[i][2], false
			if code == "" {
				code, current = got[i][3], true
			}
			if code != want.code || got[i][4] != want.name {
				t.Errorf("%s: option %d is %s %q, want %s %q", path, i+1, code, got[i][4], want.code, want.name)
			}
			if current != (want.code == lang) {
				t.Errorf("%s: option %s current=%v", path, want.code, current)
			}
		}
		if n := strings.Count(body, `aria-current="true"`); n != 1 {
			t.Errorf("%s: %d current marks, want 1", path, n)
		}
	}
}
