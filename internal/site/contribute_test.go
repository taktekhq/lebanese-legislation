package site

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContributeRoutes(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, title, lang string }{
		{"/contribute/", "ساهم", "ar"},
		{"/en/contribute/", "Contribute", "en"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
			body := rec.Body.String()
			if rec.Code != 200 {
				t.Fatalf("status %d", rec.Code)
			}
			for _, want := range []string{
				`<html lang="` + tc.lang + `"`,
				`<h1 class="page__title">` + tc.title + `</h1>`,
				`href="https://github.com/taktekhq/lebanese-legislation"`,
				`href="https://huggingface.co/spaces/taktekhq/lebanese-legal-handwriting"`,
				`href="https://huggingface.co/collections/taktekhq/lebanese-legislation"`,
				`href="https://github.com/taktekhq/lebanese-legislation-starter"`,
				`hreflang="en"`, `hreflang="ar"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Contains(body, `<form`) {
				t.Error("contribution page must not collect submissions")
			}
		})
	}
}
