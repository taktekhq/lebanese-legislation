package site

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrivacyAndTermsPages(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ path, want string }{
		{"/en/privacy/", "Privacy policy"}, {"/privacy/", "سياسة الخصوصية"},
		{"/en/terms/", "Terms of use"}, {"/terms/", "شروط الاستخدام"},
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", c.path, nil))
		body := rec.Body.String()
		if rec.Code != 200 || !strings.Contains(body, `<h1 class="page__title">`+c.want+`</h1>`) {
			t.Errorf("GET %s: %d, h1 %q missing", c.path, rec.Code, c.want)
		}
		if !strings.Contains(body, `<meta name="robots" content="index, follow, max-image-preview:large">`) {
			t.Errorf("%s is not indexable", c.path)
		}
		// every page's footer links both
		for _, link := range []string{"privacy/", "terms/"} {
			if !strings.Contains(body, link+`">`) {
				t.Errorf("%s: footer does not link %s", c.path, link)
			}
		}
		if strings.Contains(body, "—") {
			t.Errorf("%s contains an em dash", c.path)
		}
	}
}
