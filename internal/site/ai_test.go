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
	composes int
	invent   bool
	received []nlsearch.Passage
}

func (f *composeFake) Compose(_ context.Context, _ string, p []nlsearch.Passage) ([]nlsearch.Para, error) {
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

func TestAIAnswerCountsQuestionsAndSaysHowLongToWait(t *testing.T) {
	s := testServer(t)
	f := &composeFake{fakeRewrite: fakeRewrite{queries: []string{"الإجازة السنوية"}}}
	s.SetMeaningSearch(f, "Google Gemini", false, 100)
	w := aiRequest(t, s, "How much annual leave does an employee get?", false)
	if !strings.Contains(w.Body.String(), "9 of 10 AI questions left this hour") {
		t.Fatalf("no counter: %.400s", w.Body.String())
	}
	// aiRequest sends no cookie, so each call is a new browser: the
	// network cap (60 an hour per IP) is what stops it.
	for range 59 {
		aiRequest(t, s, "How much annual leave does an employee get?", false)
	}
	w = aiRequest(t, s, "How much annual leave does an employee get?", false)
	if body := w.Body.String(); !strings.Contains(body, "your network") || strings.Contains(body, "ai-box__text") {
		t.Fatalf("no network limit: %.400s", body)
	}
}

func TestAILimitIsPerBrowser(t *testing.T) {
	s := testServer(t)
	f := &composeFake{fakeRewrite: fakeRewrite{queries: []string{"الإجازة السنوية"}}}
	s.SetMeaningSearch(f, "Google Gemini", false, 1000)
	ask := func(cookie string) string {
		r := httptest.NewRequest("POST", "/en/search/ai/", strings.NewReader(url.Values{"q": {"How much annual leave & pay does an employee get?"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "ll_ai", Value: cookie})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Body.String()
	}
	me, other := strings.Repeat("a", 32), strings.Repeat("b", 32)
	for range 10 {
		ask(me)
	}
	body := ask(me)
	if !strings.Contains(body, "ask again in 6 minutes") || !strings.Contains(body, "mailto:taktekbot@taktek.io?subject=More%20AI%20questions") || !strings.Contains(body, "body=How%20much%20annual%20leave%20%26%20pay") {
		t.Fatalf("no wait message or email link: %.600s", body)
	}
	if body := ask(other); !strings.Contains(body, "9 of 10 AI questions left") {
		t.Fatalf("another visitor was limited too: %.400s", body)
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
