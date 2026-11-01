package site

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
		return nlsearch.ParseComposed(`{"paragraphs":[{"text":"x","sources":["made-up"]}]}`, p)
	}
	return []nlsearch.Para{{Text: "Yes, within limits.", Sources: []string{p[0].ID}}}, nil
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
		if w.Code != 200 || !strings.Contains(body, "Yes, within limits.") || !strings.Contains(body, `href="#ai-src-1"`) || !strings.Contains(body, `id="ai-src-1"`) {
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
	paras, src := numberSources([]nlsearch.Para{{Text: "x", Sources: []string{"s3", "s1"}}, {Text: "y", Sources: []string{"s2", "s1"}}}, byID)
	if len(src) != 2 || src[0].Path != "/ruling/9/" || len(paras[1].Refs) != 1 || paras[1].Refs[0] != 2 {
		t.Fatalf("got %+v %+v", paras, src)
	}
}
