package site

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
)

type composeFake struct {
	fakeRewrite
	expands  int
	modes    []string
	sizes    []int
	composes int
	invent   bool
	received []nlsearch.Passage
}

func (f *composeFake) Expand(_ context.Context, _ string, _, _ []string) (nlsearch.Plan, error) {
	f.expands++
	return nlsearch.Plan{Queries: []string{"الإجازة المرضية"}}, nil
}

func (f *composeFake) ComposeWith(_ context.Context, _ string, p []nlsearch.Passage, m nlsearch.Mode) ([]nlsearch.Para, error) {
	f.modes = append(f.modes, m.Name)
	f.sizes = append(f.sizes, len(p))
	f.composes++
	f.received = p
	if len(p) == 0 {
		return []nlsearch.Para{}, nil
	}
	if f.invent {
		return nlsearch.ParseComposed(`{"paragraphs":[{"text":"x","quotes":[{"source":"made-up","quote":"invented legal answer"}]}]}`, p)
	}
	q := string([]rune(p[0].Text)[:min(40, len([]rune(p[0].Text)))])
	return []nlsearch.Para{{Text: "Yes, within limits.", Quotes: []nlsearch.Cite{{Source: p[0].ID, Quote: q}}, Sources: []string{p[0].ID}}}, nil
}

func aiRequest(t *testing.T, s *Server, q string, empty bool) *httptest.ResponseRecorder {
	t.Helper()
	v := url.Values{"q": {q}}
	if empty {
		v.Set("empty", "1")
	}
	r := httptest.NewRequest("POST", "/en/search/ai/", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestAIAnswerCitesOurSourcesAndIsCached(t *testing.T) {
	s := testServer(t)
	f := &composeFake{fakeRewrite: fakeRewrite{queries: []string{"الإجازة السنوية", "قانون العمل"}}}
	s.SetMeaningSearch(f, "Google Gemini", false, 10)
	for range 2 {
		w := aiRequest(t, s, "How much annual leave does an employee get?", true)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "Yes, within limits.") || !strings.Contains(body, `href="#ai-src-1"`) || !strings.Contains(body, `id="ai-src-1"`) || !strings.Contains(body, "ai-box__quote") {
			t.Fatalf("no cited answer: %d %.400s", w.Code, body)
		}
		if !strings.Contains(body, "data-ai-results") {
			t.Fatal("empty=1 must bring the texts the meaning search found")
		}
	}
	if f.composes != 1 || len(f.received) == 0 {
		t.Fatalf("compose/cache wrong: %d passages=%d", f.composes, len(f.received))
	}
}

func TestAIAnswerRefusesLookupsAndInventedSources(t *testing.T) {
	s := testServer(t)
	f := &composeFake{fakeRewrite: fakeRewrite{queries: []string{"قانون العمل"}}, invent: true}
	s.SetMeaningSearch(f, "Google Gemini", false, 10)
	if w := aiRequest(t, s, "قانون العمل", false); w.Code != 400 || f.composes != 0 {
		t.Fatalf("a lookup got an AI answer: %d", w.Code)
	}
	w := aiRequest(t, s, "How much annual leave does an employee get?", false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "ai-box__text") || !strings.Contains(w.Body.String(), "not available") {
		t.Fatalf("invented source shown: %.300s", w.Body.String())
	}
}

func TestNumberSourcesSharesOnePagePerNumber(t *testing.T) {
	byID := map[string]answerSource{
		"s1": {Path: "/law/1/article/2/", Title: "a"},
		"s2": {Path: "/law/1/article/2/", Title: "a (fr)"},
		"s3": {Path: "/ruling/9/", Title: "r"},
	}
	c := func(id, q string) nlsearch.Cite { return nlsearch.Cite{Source: id, Quote: q} }
	paras, src := numberSources([]nlsearch.Para{{Text: "x", Quotes: []nlsearch.Cite{c("s3", "r1"), c("s1", "a1")}}, {Text: "y", Quotes: []nlsearch.Cite{c("s2", "a2"), c("s1", "a1")}}}, byID)
	if len(src) != 2 || src[0].Path != "/ruling/9/" || len(paras[1].Refs) != 1 || paras[1].Refs[0] != 2 || strings.Join(src[1].Quotes, ",") != "a1,a2" {
		t.Fatalf("got %+v %+v", paras, src)
	}
}

// askAs sends a question from one device (its ll_ai cookie) and IP.
func askAs(s *Server, cookie, ip, q string) string {
	r := httptest.NewRequest("POST", "/en/search/ai/", strings.NewReader(url.Values{"q": {q}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("CF-Connecting-IP", ip)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "ll_ai", Value: cookie})
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w.Body.String()
}

func TestAILimitIsTenADayPerDevice(t *testing.T) {
	s := testServer(t)
	f := &composeFake{fakeRewrite: fakeRewrite{queries: []string{"الإجازة السنوية"}}}
	s.SetMeaningSearch(f, "Google Gemini", false, 1000)
	q := "How much annual leave & pay does an employee get?"
	me, other := strings.Repeat("a", 32), strings.Repeat("b", 32)
	if body := askAs(s, me, "1.1.1.1", q); !strings.Contains(body, "9 of 10 AI questions left today") {
		t.Fatalf("no counter: %.400s", body)
	}
	for range 9 {
		askAs(s, me, "1.1.1.1", q)
	}
	body := askAs(s, me, "1.1.1.1", q)
	if !strings.Contains(body, "used your 10 AI questions for today") || !strings.Contains(body, "midnight Beirut time") || strings.Contains(body, "ai-box__text") ||
		!strings.Contains(body, "mailto:nizar@taktek.io?subject=Urgent%20question") || !strings.Contains(body, "body=How%20much%20annual%20leave%20%26%20pay") {
		t.Fatalf("no limit message or email link: %.800s", body)
	}
	// A new device on the same network is not affected.
	if body := askAs(s, other, "1.1.1.1", q); !strings.Contains(body, "9 of 10 AI questions left today") {
		t.Fatalf("another device was limited too: %.400s", body)
	}
}

func TestAIBansAnIPAfterAHundredADay(t *testing.T) {
	s := testServer(t)
	f := &composeFake{fakeRewrite: fakeRewrite{queries: []string{"الإجازة السنوية"}}}
	s.SetMeaningSearch(f, "Google Gemini", false, 10000)
	q := "How much annual leave does an employee get?"
	for range 99 {
		askAs(s, "", "6.6.6.6", q) // a script: a new device every time
	}
	body := askAs(s, "", "6.6.6.6", q)
	if !strings.Contains(body, "blocked for your internet connection") || !strings.Contains(body, "mailto:nizar@taktek.io?subject=Appeal") || !strings.Contains(body, "IP%3A%206.6.6.6") {
		t.Fatalf("no ban message: %.800s", body)
	}
	if body := askAs(s, strings.Repeat("c", 32), "6.6.6.6", q); !strings.Contains(body, "blocked") {
		t.Fatal("ban did not stick")
	}
	if body := askAs(s, strings.Repeat("c", 32), "7.7.7.7", q); strings.Contains(body, "blocked") {
		t.Fatal("ban hit another IP")
	}
}

func TestAISiteLimitIsHonest(t *testing.T) {
	s := testServer(t)
	f := &composeFake{fakeRewrite: fakeRewrite{queries: []string{"الإجازة السنوية"}}}
	s.SetMeaningSearch(f, "Google Gemini", false, 3) // room for one question (2 calls)
	q := "How much annual leave does an employee get?"
	askAs(s, strings.Repeat("d", 32), "8.8.8.8", q)
	body := askAs(s, strings.Repeat("e", 32), "9.9.9.9", q)
	if !strings.Contains(body, "limit for the whole site") || !strings.Contains(body, "mailto:nizar@taktek.io") {
		t.Fatalf("no honest site limit: %.600s", body)
	}
}

func TestUntilMidnightIsBeirutTime(t *testing.T) {
	now := time.Date(2026, 11, 1, 21, 30, 0, 0, time.UTC) // 23:30 in Beirut (UTC+2)
	if got := untilMidnight(now); got != 30*time.Minute {
		t.Fatalf("got %v", got)
	}
}

func TestAIVisitorCookie(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/search/ai/", nil)
	id := aiVisitor(w, r)
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Name != "ll_ai" || !c[0].HttpOnly || "v:"+c[0].Value != id {
		t.Fatalf("cookie: %+v %s", c, id)
	}
	r.AddCookie(&http.Cookie{Name: "ll_ai", Value: "<script>"})
	if got := aiVisitor(httptest.NewRecorder(), r); got == "v:<script>" {
		t.Fatal("accepted a forged cookie value")
	}
}

func TestAIQuestionGoesToPostHog(t *testing.T) {
	s := testServer(t)
	var mu sync.Mutex
	var got []map[string]any
	ph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Batch []map[string]any }
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		got = append(got, b.Batch...)
		mu.Unlock()
	}))
	defer ph.Close()
	s.SetPostHog("k", ph.URL)
	f := &composeFake{fakeRewrite: fakeRewrite{queries: []string{"الإجازة السنوية"}}}
	s.SetMeaningSearch(f, "Google Gemini", false, 10)
	v := url.Values{"q": {"How much annual leave does an employee get?"}, "ph": {"visitor-1"}}
	r := httptest.NewRequest("POST", "/en/search/ai/", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.ServeHTTP(httptest.NewRecorder(), r)
	for range 50 {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0]["event"] != "ai_question" {
		t.Fatalf("events: %+v", got)
	}
	p := got[0]["properties"].(map[string]any)
	if p["outcome"] != "answered" || p["distinct_id"] != "visitor-1" || p["question_lang"] != "latin" || p["questions_left"] != float64(9) || p["question"] == nil {
		t.Fatalf("properties: %+v", p)
	}
}

func TestDeepAnswerReadsMoreOnceADay(t *testing.T) {
	s := testServer(t)
	f := &composeFake{fakeRewrite: fakeRewrite{queries: []string{"الإجازة السنوية"}}}
	s.SetMeaningSearch(f, "Google Gemini", false, 1000)
	q := "How much annual leave does an employee get?"
	me := strings.Repeat("f", 32)
	if body := askAs(s, me, "2.2.2.2", q); !strings.Contains(body, "data-ai-deep") || !strings.Contains(body, "1 left today") {
		t.Fatalf("no deep button: %.600s", body)
	}
	deep := func() string {
		r := httptest.NewRequest("POST", "/en/search/ai/deep/", strings.NewReader(url.Values{"q": {q}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("CF-Connecting-IP", "2.2.2.2")
		r.AddCookie(&http.Cookie{Name: "ll_ai", Value: me})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Body.String()
	}
	body := deep()
	if !strings.Contains(body, "Deep answer") || !strings.Contains(body, "ai-box__text") || strings.Contains(body, "data-ai-deep") {
		t.Fatalf("no deep answer: %.600s", body)
	}
	if f.expands != 1 || f.modes[len(f.modes)-1] != "deep" || f.sizes[len(f.sizes)-1] <= f.sizes[0] {
		t.Fatalf("deep did not search more: expands=%d modes=%v sizes=%v", f.expands, f.modes, f.sizes)
	}
	if body := deep(); !strings.Contains(body, "used today's deep answer") {
		t.Fatalf("second deep answer allowed: %.400s", body)
	}
	if body := askAs(s, me, "2.2.2.2", q); !strings.Contains(body, "ai-box__text") || strings.Contains(body, "data-ai-deep") {
		t.Fatalf("quick answers stopped or deep button still shown: %.400s", body)
	}
}

func TestVectorPassagesComeFirst(t *testing.T) {
	v := []answerSource{{Passage: nlsearch.Passage{Text: "v1"}, Path: "/a"}, {Passage: nlsearch.Passage{Text: "v2"}, Path: "/b"}}
	k := []answerSource{{Passage: nlsearch.Passage{Text: "k1"}, Path: "/c"}, {Passage: nlsearch.Passage{Text: "v1"}, Path: "/a"}}
	got := vectorFirst(v, k, 3)
	if len(got) != 3 || got[0].Text != "v1" || got[1].Text != "v2" || got[2].Text != "k1" || got[2].ID != "s3" {
		t.Fatalf("got %+v", got)
	}
}
