package site

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// The static export keeps, per route, the page template a page executed and
// the strings Go code looked up for it.
func TestRecordDeps(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/privacy/", "/en/privacy/"} {
		req, done := RecordDeps(httptest.NewRequest(http.MethodGet, path, nil))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		names := done()
		for _, want := range []string{"set:privacy", "key:privacy.title", "key:privacy.desc", "key:brand.name"} {
			if !slices.Contains(names, want) {
				t.Errorf("%s: %q not noted in %v", path, want, names)
			}
		}
	}
	// Without a record nothing is noted and nothing breaks.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/privacy/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unrecorded: status %d", rec.Code)
	}
}

func TestRoute(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"/privacy/":                    "GET /privacy/{$}",
		"/en/privacy/":                 "GET /privacy/{$}",
		"/law/12/article/3/":           "GET /law/{id}/article/{n}/{$}",
		"/en/decree/12/article/3-bis/": "GET /decree/{id}/article/{n}/{$}",
		"/topic/inheritance/":          "GET /topic/{slug}/{$}",
	} {
		if got := s.Route(path); got != want {
			t.Errorf("Route(%s) = %q, want %q", path, got, want)
		}
	}
}
