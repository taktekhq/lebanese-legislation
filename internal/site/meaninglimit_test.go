package site

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMeaningBudgetAndCache(t *testing.T) {
	f := &fakeRewrite{queries: []string{"قانون العمل"}}
	m := newMeaningSearch(f, "OpenAI", true, 1)
	for range 2 {
		if _, err := m.Rewrite(t.Context(), "dismissal"); err != nil {
			t.Fatal(err)
		}
	}
	if f.calls != 1 {
		t.Fatal("cache repeated paid request")
	}
	if _, err := m.Rewrite(t.Context(), "another question"); err == nil {
		t.Fatal("daily call allowance bypassed")
	}
	if f.calls != 1 {
		t.Fatal("called provider over budget")
	}
}

func TestSearchNeverRequiresAMode(t *testing.T) {
	s := testServer(t)
	f := &fakeRewrite{queries: []string{"قانون العمل"}}
	s.SetMeaningSearch(f, "OpenAI", true, 5)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/en/search/?q=Can+my+employer+fire+me&meaning=1", nil)
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || f.calls != 0 {
		t.Fatalf("GET called model: %d %d", rec.Code, f.calls)
	}
	for _, origin := range []string{"https://evil.example", ""} {
		data := url.Values{"q": {"Can my employer fire me?"}, "meaning": {"1"}}
		req = httptest.NewRequest("POST", "/en/search/", strings.NewReader(data.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec = httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if origin != "" {
			if rec.Code != 403 || f.calls != 0 {
				t.Fatal("cross-site request allowed")
			}
		} else if rec.Code != 200 || f.calls != 0 || !strings.Contains(rec.Body.String(), "data-grounded-answer") {
			t.Fatalf("POST failed: %d calls=%d", rec.Code, f.calls)
		}
	}
}

func TestMeaningCancelled(t *testing.T) {
	f := &fakeRewrite{queries: []string{"قانون العمل"}}
	m := newMeaningSearch(f, "Gemini", false, 5)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.Rewrite(ctx, "q"); err == nil {
		t.Fatal("cancelled request called model")
	}
	if f.calls != 0 {
		t.Fatal("called model after cancellation")
	}
}

func TestMeaningSharedInputLimit(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/en/search/?q="+strings.Repeat("z", 250), nil))
	if rec.Code != 200 {
		t.Fatalf("shared 500-character input rejected ordinary search: %d", rec.Code)
	}
}
