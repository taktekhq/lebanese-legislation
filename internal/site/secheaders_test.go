package site

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"
)

// The Worker's policy and this one must be the same string.
func TestCSPMatchesWorker(t *testing.T) {
	b, err := os.ReadFile("../../workers/static-site/src/headers.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`export const CSP = "([^"]*)"`).FindSubmatch(b)
	if m == nil || string(m[1]) != CSP {
		t.Fatalf("site.CSP differs from workers/static-site/src/headers.js")
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/own" {
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Header().Get("Content-Security-Policy") != CSP || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("headers: %v", rec.Header())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/own", nil))
	if rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatal("a handler's own value was overwritten")
	}
}
