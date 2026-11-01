package site

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsBot(t *testing.T) {
	for ua, want := range map[string]bool{
		"": true,
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)":                               true,
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.2; +https://openai.com/gptbot)": true,
		"curl/8.7.1": true,
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1": false,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36":                   false,
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("User-Agent", ua)
		if got := IsBot(r); got != want {
			t.Errorf("%q: got %v", ua, got)
		}
	}
}

func TestNormQuery(t *testing.T) {
	if got := NormQuery("  Code   CIVIL  "); got != "code civil" {
		t.Errorf("got %q", got)
	}
	if got := NormQuery(" قانون  الايجارات "); got != "قانون الايجارات" {
		t.Errorf("got %q", got)
	}
}

// The record holds no IP address or user agent, and the day adds up with
// people and robots apart.
func TestEventsAndStats(t *testing.T) {
	dir := t.TempDir()
	ev, err := OpenEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{ev: ev}
	human := "Mozilla/5.0 (Macintosh) Safari/604.1"
	req := func(ua string) *http.Request {
		r := httptest.NewRequest("GET", "/search/?q=x", nil)
		r.Header.Set("User-Agent", ua)
		r.RemoteAddr = "203.0.113.7:5555"
		return r
	}
	s.track(req(human), map[string]any{"k": "search", "q": "ايجار", "n": 12})
	s.track(req(human), map[string]any{"k": "search", "q": "ايجار", "n": 12})
	s.track(req(human), map[string]any{"k": "search", "q": "قانون الذكاء", "n": 0})
	s.track(req("Googlebot"), map[string]any{"k": "search", "q": "spam", "n": 0})
	code := "Code of Obligations and Contracts"
	l := &Law{ID: 244226, Title: "قانون الموجبات والعقود", Kind: "قانون", Code: &code}
	s.trackRead(req(human), l, "")
	s.trackRead(req(human), l, "23")
	s.trackRead(req("GPTBot/1.2"), l, "1")
	head := req(human)
	head.Method = http.MethodHead
	s.track(head, map[string]any{"k": "search", "q": "head", "n": 1})

	day := time.Now().UTC().Format("2006-01-02")
	raw, err := os.ReadFile(filepath.Join(dir, "events", day+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"203.0.113.7", "Mozilla", "Googlebot", "GPTBot"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the record holds %q", leak)
		}
	}

	st, err := UsageStats(dir, day)
	if err != nil {
		t.Fatal(err)
	}
	if st.Searches != 3 || st.SearchesBots != 1 || st.UniqueQueries != 2 || st.ZeroResults != 1 {
		t.Errorf("searches: %+v", st)
	}
	if st.TopQueries[0].Key != "ايجار" || st.TopQueries[0].N != 2 || st.TopZeroQueries[0].Key != "قانون الذكاء" {
		t.Errorf("top: %+v / %+v", st.TopQueries, st.TopZeroQueries)
	}
	if st.Reads != 2 || st.ReadsBots != 1 || st.ArticleReads != 1 || st.TopLaws[0].Key != "244226" || st.TopCodes[0].Key != code {
		t.Errorf("reads: %+v", st)
	}
}

func TestNotify(t *testing.T) {
	dir := t.TempDir()
	ev, _ := OpenEvents(dir)
	s := &Server{ev: ev}
	post := func(form url.Values) string {
		r := httptest.NewRequest("POST", "/api/notify", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.notify(w, r)
		return w.Header().Get("Location")
	}
	if got := post(url.Values{"email": {"a@example.com"}}); !strings.Contains(got, "notify=bad") {
		t.Errorf("no consent: %s", got)
	}
	if got := post(url.Values{"email": {"not an email"}, "consent": {"yes"}}); !strings.Contains(got, "notify=bad") {
		t.Errorf("bad email: %s", got)
	}
	if got := post(url.Values{"email": {"robot@example.com"}, "consent": {"yes"}, "website": {"x"}}); !strings.Contains(got, "notify=ok") {
		t.Errorf("honeypot: %s", got)
	}
	if got := post(url.Values{"email": {"a@example.com"}, "consent": {"yes"}, "from": {"ai"}}); !strings.Contains(got, "notify=ok") {
		t.Errorf("good: %s", got)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "notify.jsonl"))
	if n := strings.Count(string(b), "\n"); n != 1 || !strings.Contains(string(b), "a@example.com") || strings.Contains(string(b), "robot@") {
		t.Errorf("notify.jsonl: %s", b)
	}
	st, _ := UsageStats(dir, time.Now().UTC().Format("2006-01-02"))
	if st.NotifySignups != 1 || st.NotifyTotal != 1 {
		t.Errorf("notify counts: %+v", st)
	}
}
