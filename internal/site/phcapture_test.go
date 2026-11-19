package site

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// phSink is a stand-in PostHog: every captured batch lands on the channel.
func phSink(t *testing.T) (*httptest.Server, chan map[string]any) {
	t.Helper()
	got := make(chan map[string]any, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/batch/" {
			t.Errorf("posted to %s, want /batch/", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(b, &body); err != nil {
			t.Errorf("body is not JSON: %v", err)
		}
		got <- body
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

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

// nothingSent fails when PostHog got an event.
func nothingSent(t *testing.T, got chan map[string]any) {
	t.Helper()
	select {
	case body := <-got:
		t.Fatalf("an error was sent to PostHog: %v", body)
	case <-time.After(200 * time.Millisecond):
	}
}

// Server errors go to the server log only, never to PostHog (19 Nov 2026):
// one line with the kind, the method, the address and the error text.
func TestFailLogsAndSendsNothing(t *testing.T) {
	srv, got := phSink(t)
	s := &Server{}
	s.SetPostHog("phc_test", srv.URL)
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
	nothingSent(t, got)
}

func TestPanicLoggedAndRecovered(t *testing.T) {
	srv, got := phSink(t)
	s := &Server{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("nil map") })}
	s.SetPostHog("phc_test", srv.URL)
	w := httptest.NewRecorder()
	out := logged(t, func() { s.ServeHTTP(w, httptest.NewRequest("POST", "/search?q=x", nil)) })
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", w.Code)
	}
	if out != "panic POST /search?q=x: nil map\n" {
		t.Errorf("log %q", out)
	}
	nothingSent(t, got)
}

func TestAPIFailLogsAndSendsNothing(t *testing.T) {
	srv, got := phSink(t)
	s := &Server{}
	s.SetPostHog("phc_test", srv.URL)
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
	nothingSent(t, got)
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

func TestPostHogScriptServed(t *testing.T) {
	s, err := newServer(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/ph.js", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `api_host: "/ph"`) {
		t.Fatalf("GET /ph.js: %d, body has api_host: %v", w.Code, strings.Contains(w.Body.String(), "api_host"))
	}
}

// The privacy text names PostHog in both languages.
func TestPostHogPrivacy(t *testing.T) {
	for _, lang := range []string{"en", "ar"} {
		raw, err := os.ReadFile("i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(m["about.privacy_p1"], "PostHog") {
			t.Errorf("%s about.privacy_p1 does not mention PostHog", lang)
		}
	}
}
