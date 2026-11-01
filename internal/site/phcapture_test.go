package site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
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

func firstEvent(t *testing.T, got chan map[string]any) map[string]any {
	t.Helper()
	select {
	case body := <-got:
		batch := body["batch"].([]any)
		return batch[0].(map[string]any)
	case <-time.After(3 * time.Second):
		t.Fatal("nothing was sent to PostHog")
		return nil
	}
}

func TestCaptureNoKey(t *testing.T) {
	srv, got := phSink(t)
	s := &Server{}
	s.SetPostHog("", srv.URL)
	s.fail(httptest.NewRecorder(), httptest.NewRequest("GET", "/law/1", nil), errors.New("boom"))
	select {
	case <-got:
		t.Fatal("sent an event with no key")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestFailCapturesException(t *testing.T) {
	srv, got := phSink(t)
	s := &Server{}
	s.SetPostHog("phc_test", srv.URL)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/law/123?q=x", nil)
	r.Header.Set("User-Agent", "Mozilla/5.0 test")
	s.fail(w, r, errors.New("template: boom"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", w.Code)
	}
	ev := firstEvent(t, got)
	if ev["event"] != "$exception" {
		t.Fatalf("event %v, want $exception", ev["event"])
	}
	p := ev["properties"].(map[string]any)
	for _, key := range []string{"$current_url", "$pathname", "$ip", "$user_agent"} {
		if _, ok := p[key]; ok {
			t.Errorf("private property %s was sent", key)
		}
	}
	list := p["$exception_list"].([]any)
	if list[0].(map[string]any)["value"] != "Server request failed" {
		t.Errorf("$exception_list %v", list)
	}
	if p["status"].(float64) != 500 {
		t.Errorf("status %v", p["status"])
	}
}

func TestPanicRecovered(t *testing.T) {
	srv, got := phSink(t)
	s := &Server{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("nil map") })}
	s.SetPostHog("phc_test", srv.URL)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/search?q=x", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", w.Code)
	}
	ev := firstEvent(t, got)
	p := ev["properties"].(map[string]any)
	if ev["event"] != "$exception" || p["$exception_list"].([]any)[0].(map[string]any)["value"] != "Server request failed" {
		t.Fatalf("got %v", ev)
	}
}

func TestCaptureDoesNotUseBrowserDistinctID(t *testing.T) {
	srv, got := phSink(t)
	s := &Server{}
	s.SetPostHog("phc_test", srv.URL)
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "ph_phc_test_posthog", Value: `%7B%22distinct_id%22%3A%22abc-123%22%7D`})
	s.fail(httptest.NewRecorder(), r, errors.New("x"))
	p := firstEvent(t, got)["properties"].(map[string]any)
	if p["distinct_id"] != "lebaneselegislation-server" {
		t.Errorf("distinct_id %v, want anonymous server identifier", p["distinct_id"])
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

func TestCaptureSkipsClientDisconnects(t *testing.T) {
	srv, got := phSink(t)
	s := &Server{}
	s.SetPostHog("phc_test", srv.URL)
	s.fail(httptest.NewRecorder(), httptest.NewRequest("GET", "/law/1", nil), fmt.Errorf("query: %w", context.Canceled))
	select {
	case <-got:
		t.Fatal("a client disconnect was sent as an error")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestCaptureIsRateLimited(t *testing.T) {
	srv, got := phSink(t)
	s := &Server{}
	s.SetPostHog("phc_test", srv.URL)
	for i := 0; i < 50; i++ {
		s.fail(httptest.NewRecorder(), httptest.NewRequest("GET", "/law/1", nil), errors.New("db down"))
	}
	n := 0
	deadline := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case <-got:
			n++
		case <-deadline:
			break loop
		}
	}
	if n < 1 || n > phBurst {
		t.Fatalf("sent %d events for 50 errors in a burst, want 1..%d", n, phBurst)
	}
}

func TestAPIFailCapturesException(t *testing.T) {
	srv, got := phSink(t)
	s := &Server{}
	s.SetPostHog("phc_test", srv.URL)
	w := httptest.NewRecorder()
	s.apiFail(w, httptest.NewRequest("GET", "/api/v1/law/1/", nil), errors.New("db down"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", w.Code)
	}
	if ev := firstEvent(t, got); ev["event"] != "$exception" {
		t.Fatalf("got %v", ev["event"])
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
