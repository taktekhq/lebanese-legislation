package site

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// logged runs f and returns what it wrote to the server log.
func logged(t *testing.T, f func()) string {
	t.Helper()
	var buf bytes.Buffer
	old, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() { log.SetOutput(old); log.SetFlags(flags) }()
	f()
	return buf.String()
}

// Server errors go to the server log: one line with the kind, the method,
// the address and the error text.
func TestFailLogs(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	out := logged(t, func() {
		s.fail(w, httptest.NewRequest("GET", "/law/123?q=x", nil), errors.New("template: boom"))
	})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", w.Code)
	}
	if out != "error GET /law/123?q=x: template: boom\n" {
		t.Errorf("log %q", out)
	}
}

func TestPanicLoggedAndRecovered(t *testing.T) {
	s := &Server{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("nil map") })}
	w := httptest.NewRecorder()
	out := logged(t, func() { s.ServeHTTP(w, httptest.NewRequest("POST", "/search?q=x", nil)) })
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", w.Code)
	}
	if out != "panic POST /search?q=x: nil map\n" {
		t.Errorf("log %q", out)
	}
}

func TestAPIFailLogs(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	out := logged(t, func() {
		s.apiFail(w, httptest.NewRequest("GET", "/api/v1/law/1/", nil), errors.New("db down"))
	})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", w.Code)
	}
	if out != "api error GET /api/v1/law/1/: db down\n" {
		t.Errorf("log %q", out)
	}
}

// A request the mux matched also names its route pattern.
func TestLogErrorNamesTheRoute(t *testing.T) {
	r := httptest.NewRequest("GET", "/en/law/7/", nil)
	r.Pattern = "GET /{lang}/law/{id}/"
	out := logged(t, func() { logError(r, "answer render", errors.New("broken pipe")) })
	if out != "answer render GET /en/law/7/ (route GET /{lang}/law/{id}/): broken pipe\n" {
		t.Errorf("log %q", out)
	}
}

// PostHog is gone (23 Nov 2026): no /ph.js, and the privacy text says we
// do not use it, in every language.
func TestNoPostHog(t *testing.T) {
	s, err := newServer(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/ph.js", nil))
	if w.Code == http.StatusOK {
		t.Errorf("GET /ph.js: %d, want no script", w.Code)
	}
	for _, lang := range []string{"en", "ar", "fr"} {
		raw, err := os.ReadFile("i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(m["about.privacy_p1"], "PostHog") {
			t.Errorf("%s about.privacy_p1 should say we no longer use PostHog", lang)
		}
	}
}
