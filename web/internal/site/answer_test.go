package site

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/web/internal/nlsearch"
)

type evidenceFake struct {
	fakeRewrite
	selects  int
	invent   bool
	received []nlsearch.Passage
}

func (f *evidenceFake) Select(_ context.Context, _ string, p []nlsearch.Passage) ([]nlsearch.Quote, error) {
	f.selects++
	f.received = p
	if f.invent {
		return []nlsearch.Quote{{SourceID: "made-up", Quote: "invented legal answer"}}, nil
	}
	if len(p) == 0 {
		return []nlsearch.Quote{}, nil
	}
	return []nlsearch.Quote{{SourceID: p[0].ID, Quote: string([]rune(p[0].Text)[:min(100, len([]rune(p[0].Text)))])}}, nil
}

func answerRequest(t *testing.T, s *Server, q, origin string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/en/search/answer/", strings.NewReader(url.Values{"q": {q}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestUnifiedSearchHasNoModeChoice(t *testing.T) {
	s := testServer(t)
	f := &evidenceFake{fakeRewrite: fakeRewrite{queries: []string{"قانون العمل"}}}
	s.SetMeaningSearch(f, "Google Gemini", false, 10)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/en/search/?q=labor", nil))
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, `name="meaning"`) || !strings.Contains(body, "data-grounded-answer") || f.calls != 0 {
		t.Fatalf("GET flow wrong: status=%d calls=%d", w.Code, f.calls)
	}
	if !strings.Contains(body, "/190374/") || strings.Index(body, "/190374/") > strings.Index(body, "data-grounded-answer") {
		t.Fatal("exact source does not precede automatic answer")
	}
}

func TestAnswerIsGroundedAndCached(t *testing.T) {
	s := testServer(t)
	f := &evidenceFake{fakeRewrite: fakeRewrite{queries: []string{"الإجازة السنوية", "قانون العمل"}}}
	s.SetMeaningSearch(f, "Google Gemini", false, 10)
	for range 2 {
		w := answerRequest(t, s, "How much annual leave does an employee get?", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "data-evidence-quote") || (!strings.Contains(w.Body.String(), "/law/") && !strings.Contains(w.Body.String(), "/ruling/")) {
			t.Fatalf("no sourced answer: %d %.300s", w.Code, w.Body.String())
		}
	}
	if f.selects != 1 || len(f.received) == 0 {
		t.Fatalf("selection/cache wrong: %d passages=%d", f.selects, len(f.received))
	}
}

func TestAnswerRejectsInventedQuotesAndMissingTexts(t *testing.T) {
	s := testServer(t)
	f := &evidenceFake{fakeRewrite: fakeRewrite{queries: []string{"قانون العمل"}}, invent: true}
	s.SetMeaningSearch(f, "Google Gemini", false, 10)
	w := answerRequest(t, s, "dismissal rights", "")
	if strings.Contains(w.Body.String(), "invented legal answer") || strings.Contains(w.Body.String(), "data-evidence-quote") {
		t.Fatal("invented evidence displayed")
	}
	before := f.calls + f.selects
	w = answerRequest(t, s, "القرار رقم 513 تاريخ 11/7/2005", "")
	if f.calls+f.selects != before || strings.Contains(w.Body.String(), "data-evidence-quote") {
		t.Fatal("missing citation substituted by AI")
	}
	w = answerRequest(t, s, "question", "https://evil.example")
	if w.Code != 403 {
		t.Fatal("cross-site answer request allowed")
	}
}

func TestEvidenceSelectionSharesDailyBudget(t *testing.T) {
	f := &evidenceFake{fakeRewrite: fakeRewrite{queries: []string{"قانون العمل"}}}
	m := newMeaningSearch(f, "Google Gemini", false, 1)
	if _, err := m.Rewrite(t.Context(), "question"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.selectEvidence(t.Context(), "question", []nlsearch.Passage{{ID: "a", Text: "source text"}}); err == nil {
		t.Fatal("selector bypassed shared daily allowance")
	}
	if f.selects != 0 {
		t.Fatal("called model after budget exhaustion")
	}
}

func TestAnswerDoesNotUseRepealedOrPendingText(t *testing.T) {
	s := testServer(t)
	for _, condition := range []string{"state='repealed'", "pending"} {
		var id int
		var number string
		err := s.db.QueryRow(t.Context(), "SELECT law_id,number FROM article_in_force WHERE "+condition+" LIMIT 1").Scan(&id, &number)
		if err != nil {
			t.Log("no fixture for", condition)
			continue
		}
		if _, _, _, _, err = s.answerArticle(t.Context(), id, number); err == nil {
			t.Fatalf("used unsupported text %d/%s (%s)", id, number, condition)
		}
	}
}
