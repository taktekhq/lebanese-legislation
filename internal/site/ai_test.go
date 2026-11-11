package site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/indexing"
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

// An answer asks whether it helped. The rating goes from the browser to
// PostHog, so the server only renders the question and the two thumbs, and
// only under a real answer.
func TestAIAnswerAsksForARating(t *testing.T) {
	s := testServer(t)
	f := &composeFake{fakeRewrite: fakeRewrite{queries: []string{"الإجازة السنوية"}}}
	s.SetMeaningSearch(f, "Google Gemini", false, 1000)
	q := "How much annual leave does an employee get?"
	me := strings.Repeat("c", 32)
	body := askAs(s, me, "1.1.1.1", q)
	for _, want := range []string{`data-ai-rate data-mode="quick"`, `data-rate="up"`, `aria-label="Helpful"`, `data-rate="down"`, `aria-label="Not helpful"`, "Was this answer helpful?", `data-ai-why hidden`, `data-reason="wrong_law"`, `data-reason="other" aria-expanded="false"`, `data-ai-note hidden`, `class="ai-box__form"`, `data-ai-thanks role="status"></p>`} {
		if !strings.Contains(body, want) {
			t.Fatalf("no %q under the answer: %.800s", want, body)
		}
	}
	for range 10 {
		body = askAs(s, me, "1.1.1.1", q)
	}
	if !strings.Contains(body, "used your 10 AI questions") || strings.Contains(body, "data-ai-rate") {
		t.Fatalf("a limit message must not ask for a rating: %.600s", body)
	}
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
	r.Header.Set("CF-Connecting-IP", "203.0.113.5")
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
	// The visitor's IP, not the container's own address PostHog would see.
	if p["$ip"] != "203.0.113.5" || p["$geoip_disable"] != false {
		t.Fatalf("visitor IP: %+v", p)
	}
}

// Each model call of a question goes to PostHog's LLM analytics as a
// $ai_generation: model, tokens, time and status, tied to the question by a
// trace id. The question, the passages and the answer never do. No database:
// trackAI gets the usage a question would have collected.
func TestAIGenerationsGoToPostHog(t *testing.T) {
	const question = "How much annual leave does an employee get?"
	rewrite := nlsearch.Generation{Kind: "rewrite", Provider: "gemini", Model: "gemini-3.1-flash-lite", In: 400, Out: 20, Latency: 250 * time.Millisecond, Status: 200}
	compose := nlsearch.Generation{Kind: "compose", Provider: "gemini", Model: "gemini-3.8-flash", In: 2000, Out: 100, Latency: 1500 * time.Millisecond, Status: 200}
	refused := nlsearch.Generation{Kind: "compose", Provider: "gemini", Model: "gemini-3.8-flash", Latency: 2 * time.Second, Status: 503, Failed: true}
	dead := nlsearch.Generation{Kind: "rewrite", Provider: "gemini", Model: "gemini-3.1-flash-lite", Latency: 10 * time.Second, Failed: true}

	// track runs trackAI for one question whose model calls were gens, and
	// returns the events PostHog received, with the raw bodies.
	track := func(t *testing.T, ph string, view aiView, reason error, gens ...nlsearch.Generation) (questions, generations []map[string]any, raw string) {
		t.Helper()
		var mu sync.Mutex
		var events []map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var b struct{ Batch []map[string]any }
			_ = json.Unmarshal(body, &b)
			mu.Lock()
			events, raw = append(events, b.Batch...), raw+string(body)
			mu.Unlock()
		}))
		defer srv.Close()
		s := &Server{}
		s.SetPostHog("k", srv.URL)
		v := url.Values{"q": {question}}
		if ph != "" {
			v.Set("ph", ph)
		}
		r := httptest.NewRequest("POST", "/en/search/ai/", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("CF-Connecting-IP", "203.0.113.5")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		ctx, usage := nlsearch.WithUsage(r.Context())
		for _, g := range gens {
			nlsearch.AddGeneration(ctx, g)
			if !g.Failed {
				nlsearch.AddUsage(ctx, g.In, g.Out)
			}
		}
		s.trackAI(r, question, view, usage, reason, 2*time.Second)
		want := 1 + len(gens)
		for range 100 {
			mu.Lock()
			n := len(events)
			mu.Unlock()
			if n >= want {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(30 * time.Millisecond) // anything sent beyond want
		mu.Lock()
		defer mu.Unlock()
		if len(events) != want {
			t.Fatalf("want %d events, got %d: %+v", want, len(events), events)
		}
		for _, e := range events {
			p := e["properties"].(map[string]any)
			switch e["event"] {
			case "ai_question":
				questions = append(questions, p)
			case "$ai_generation":
				generations = append(generations, p)
			default:
				t.Errorf("unexpected event %v", e["event"])
			}
		}
		if len(questions) != 1 {
			t.Fatalf("ai_question events: %+v", questions)
		}
		return questions, generations, raw
	}
	answered := aiView{Paras: []aiPara{{}}}

	t.Run("answered", func(t *testing.T) {
		questions, gens, raw := track(t, "visitor-1", answered, nil, rewrite, compose)
		q := questions[0]
		trace, _ := q["$ai_trace_id"].(string)
		if len(trace) != 32 {
			t.Fatalf("trace id %q", trace)
		}
		byKind := map[string]map[string]any{}
		for _, g := range gens {
			byKind[fmt.Sprint(g["$ai_span_name"])] = g
			if g["$ai_trace_id"] != trace || g["distinct_id"] != "visitor-1" || g["$ai_provider"] != "gemini" || g["$ai_is_error"] != false ||
				g["$ai_http_status"] != float64(200) || g["mode"] != "quick" || g["$process_person_profile"] != false || g["$geoip_disable"] != true ||
				g["$lib"] != "lebaneselegislation-server" {
				t.Errorf("generation: %+v", g)
			}
			for _, banned := range []string{"$ai_input", "$ai_output_choices", "$ai_output", "question", "$ip"} {
				if _, has := g[banned]; has {
					t.Errorf("a generation carries %s: %+v", banned, g)
				}
			}
		}
		rw, co := byKind["rewrite"], byKind["compose"]
		if rw == nil || co == nil {
			t.Fatalf("kinds: %+v", gens)
		}
		if rw["$ai_model"] != "gemini-3.1-flash-lite" || rw["$ai_input_tokens"] != float64(400) || rw["$ai_output_tokens"] != float64(20) || rw["$ai_latency"] != 0.25 {
			t.Errorf("rewrite: %+v", rw)
		}
		// The code has no price for the rewriting model: PostHog prices it.
		if _, has := rw["$ai_total_cost_usd"]; has {
			t.Errorf("rewrite carries a cost the code does not know: %+v", rw)
		}
		wantCost := 2000*aiUSDPerMIn/1e6 + 100*aiUSDPerMOut/1e6
		if cost, _ := co["$ai_total_cost_usd"].(float64); co["$ai_model"] != "gemini-3.8-flash" || co["$ai_input_tokens"] != float64(2000) ||
			co["$ai_output_tokens"] != float64(100) || co["$ai_latency"] != 1.5 || cost < wantCost*0.999 || cost > wantCost*1.001 {
			t.Errorf("compose: %+v, want cost %v", co, wantCost)
		}
		// ai_question's own numbers are as before.
		if q["outcome"] != "answered" || q["model_calls"] != float64(2) || q["tokens_in"] != float64(2400) || q["tokens_out"] != float64(120) ||
			q["distinct_id"] != "visitor-1" || q["$ip"] != "203.0.113.5" || q["cached"] != false {
			t.Errorf("ai_question changed: %+v", q)
		}
		// The question text is on ai_question only, as before.
		if n := strings.Count(raw, "annual leave"); n != 1 {
			t.Errorf("the question text was sent %d times, want 1 (ai_question only)", n)
		}
	})

	t.Run("answered from the cache", func(t *testing.T) {
		// No model was called: no generation and no trace.
		questions, gens, _ := track(t, "visitor-1", answered, nil)
		if _, has := questions[0]["$ai_trace_id"]; has || len(gens) != 0 || questions[0]["cached"] != true {
			t.Errorf("question %+v, generations %+v", questions[0], gens)
		}
	})

	t.Run("failed calls, no browser id", func(t *testing.T) {
		questions, gens, _ := track(t, "", aiView{Unavailable: true}, errors.New("answer provider HTTP 503"), rewrite, refused, dead)
		if questions[0]["outcome"] != "unavailable" || questions[0]["model_calls"] != float64(1) || len(fmt.Sprint(questions[0]["$ai_trace_id"])) != 32 {
			t.Errorf("question: %+v", questions[0])
		}
		var sawRefused, sawDead bool
		for _, g := range gens {
			if g["distinct_id"] != "lebaneselegislation-server" || g["$ai_trace_id"] != questions[0]["$ai_trace_id"] {
				t.Errorf("ids: %+v", g)
			}
			status, hasStatus := g["$ai_http_status"]
			switch {
			case g["$ai_is_error"] == true && status == float64(503):
				sawRefused = g["$ai_input_tokens"] == float64(0) && g["$ai_latency"] == 2.0 && g["$ai_total_cost_usd"] == float64(0)
			case g["$ai_is_error"] == true && !hasStatus:
				// No response came: there is no status to report.
				sawDead = g["$ai_latency"] == 10.0
			}
		}
		if !sawRefused || !sawDead {
			t.Errorf("refused %v, dead %v: %+v", sawRefused, sawDead, gens)
		}
	})

	t.Run("deep", func(t *testing.T) {
		_, gens, _ := track(t, "", aiView{Deep: true, Paras: []aiPara{{}}}, nil, compose)
		if gens[0]["mode"] != "deep" {
			t.Errorf("mode: %+v", gens[0])
		}
	})
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

// "Out of date" shows each source's "Last updated" line: the date its page
// last changed and the text that last amended it. A source we know nothing
// about gets no line.
func TestAISourcesCarryTheirDates(t *testing.T) {
	s := testServer(t)
	var law int
	if err := s.db.QueryRow(context.Background(), `SELECT m.law_id FROM amendment m
		JOIN legislation b ON b.id = m.by_law_id
		JOIN page_state p ON p.kind = 'law' AND p.id = m.law_id
		WHERE m.status IN ('applied', 'partial') AND m.by_law_id <> m.law_id
		AND coalesce(m.effective_on, b.issued_on) <= current_date AND p.changed_at >= $1
		ORDER BY m.law_id LIMIT 1`, indexing.FirstFetch).Scan(&law); err != nil {
		t.Skip("no amended text with a page date in this database")
	}
	sources := []aiSource{{answerSource: answerSource{Path: lawPath(law)}}, {answerSource: answerSource{Path: "/ruling/999999999/"}}}
	s.dateSources(httptest.NewRequest("POST", "/en/search/ai/", nil), sources)
	if u := sources[0].Updated; u == nil || u.By == nil || !strings.HasPrefix(u.Label, "Last updated") {
		t.Fatalf("text %d: updated %+v", law, u)
	}
	if sources[1].Updated != nil {
		t.Fatalf("an unknown ruling got a date: %+v", sources[1].Updated)
	}
}
