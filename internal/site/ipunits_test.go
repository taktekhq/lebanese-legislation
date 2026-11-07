package site

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
)

// The API's semantic+select path spends two units a request. One IP may not
// spend more than its daily share, and other IPs keep the rest.
func TestIPDailyUnitsCapTheAPIPath(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &evidenceFake{}
	s.SetMeaningSearch(f, "Google Gemini", false, 4000)
	m := s.meaning
	v := &vectorFake{}
	ctx := withSpender(t.Context(), "203.0.113.7")
	passages := []nlsearch.Passage{{ID: "a", Text: "b"}}
	var last error
	for i := range 5000 {
		q := fmt.Sprintf("question number %d about labour law", i)
		_, e1 := m.vectorHits(ctx, q, v)
		_, e2 := m.selectEvidence(ctx, q, passages)
		if e1 != nil && e2 != nil {
			last = e1
			break
		}
	}
	if !errors.Is(last, errIPDaily) {
		t.Fatalf("one IP was never stopped: %v", last)
	}
	if m.used != DefaultIPDailyUnits || v.calls+f.selects != DefaultIPDailyUnits {
		t.Fatalf("used %d (vector %d, select %d), want %d", m.used, v.calls, f.selects, DefaultIPDailyUnits)
	}
	other := withSpender(t.Context(), "198.51.100.9")
	if _, err := m.vectorHits(other, "how long is maternity leave", v); err != nil {
		t.Fatalf("another IP was stopped: %v", err)
	}
	if !m.budgetFor(deepWeight + 3) {
		t.Fatal("one IP drained the site-wide allowance")
	}
}

// Deep answers are weighted by their units, so a script with a new device
// every time stops after a few of them, not after 99.
func TestIPDailyUnitsWeighDeepAnswers(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &composeFake{fakeRewrite: fakeRewrite{queries: []string{"x"}}}
	s.SetMeaningSearch(f, "Google Gemini", false, 4000)
	s.SetMeaningIPDaily(60)
	m := s.meaning
	ip := "203.0.113.66"
	ctx := withSpender(t.Context(), ip)
	v := &vectorFake{}
	deep := 0
	for i := range 99 {
		q := fmt.Sprintf("question %d", i)
		_, e1 := m.Rewrite(ctx, q)
		_, _ = m.vectorHits(ctx, q, v)
		_, _ = m.expand(ctx, q, nil, nil)
		for j := range 3 {
			_, _ = m.vectorHits(ctx, fmt.Sprintf("%s phrase %d", q, j), v)
		}
		_, e2 := m.composeAnswer(ctx, q, []nlsearch.Passage{{ID: "s1", Text: "t"}}, nlsearch.Deep)
		if e1 != nil {
			break
		}
		if e2 == nil {
			deep++
		}
	}
	if deep != 3 || m.used > 60 {
		t.Fatalf("deep answers %d, units %d: want 3 within 60", deep, m.used)
	}
	// The answer box says so with its limited view, before any search,
	// for a cookie-less request as well as one with a cookie.
	for _, cookie := range []string{"", strings.Repeat("f", 32)} {
		r := httptest.NewRequest("POST", "/en/search/ai/deep/", strings.NewReader(url.Values{"q": {"How long is maternity leave?"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("CF-Connecting-IP", ip)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "ll_ai", Value: cookie})
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "ai-box__limit") {
			t.Fatalf("no limited view: %d %.400s", w.Code, w.Body.String())
		}
	}
}

func TestBannedIPSpendsNothing(t *testing.T) {
	f := &fakeRewrite{queries: []string{"x"}}
	m := newMeaningSearch(f, "Google Gemini", false, 100)
	m.bans.ban("6.6.6.6")
	if _, err := m.Rewrite(withSpender(t.Context(), "6.6.6.6"), "q"); !errors.Is(err, errIPBanned) || f.calls != 0 || m.used != 0 {
		t.Fatalf("banned IP spent: %v calls=%d used=%d", err, f.calls, m.used)
	}
}

// A call that names no IP shares one capped key, so no path spends without a cap.
func TestUntaggedSpendIsCapped(t *testing.T) {
	m := newMeaningSearch(&fakeRewrite{}, "Google Gemini", false, 1000)
	m.ipDaily = 5
	n := 0
	for range 10 {
		if m.take(t.Context(), 1) == nil {
			n++
		}
	}
	if n != 5 {
		t.Fatalf("untagged spends %d, want 5", n)
	}
}

// Every request reaches the handlers with its client IP as the spender.
func TestServeHTTPNamesTheSpender(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = spender(r.Context()) })
	r := httptest.NewRequest("GET", "/api/v1/search/?q=x&semantic=true", nil)
	r.Header.Set("CF-Connecting-IP", "203.0.113.9")
	s.ServeHTTP(httptest.NewRecorder(), r)
	if got != "203.0.113.9" {
		t.Fatalf("spender %q", got)
	}
}
