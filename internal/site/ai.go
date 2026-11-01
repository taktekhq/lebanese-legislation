package site

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
)

// The AI answer at the top of the search page, for searches that read as
// questions (IsQuestion). It is written by the model from passages of our own
// collection only, each paragraph naming the passages it rests on; the page
// links those sources. Lookups of a text never come here.

type answerComposer interface {
	Compose(context.Context, string, []nlsearch.Passage) ([]nlsearch.Para, error)
}

type composedEntry struct {
	paras []nlsearch.Para
	at    time.Time
}

type aiPara struct {
	Text string
	Refs []int // 1-based, into aiView.Sources
}

// aiSource is a cited page and the exact words of it the answer quotes.
type aiSource struct {
	answerSource
	Quotes []string
}

type aiView struct {
	Paras                []aiPara
	Sources              []aiSource
	Provider             string
	Unavailable, Missing bool
	Results              *Results
	Left, PerHour        int // questions this visitor may still ask this hour, of PerHour
	Limited              bool
	Network              bool   // limited by the network (IP) cap, not the visitor's own
	WaitMin              int    // when Limited: minutes until the next question
	Question             string // when Limited: for the "email us" link // what the meaning search found, when the page found nothing
}

// composeAnswer is Compose with a content-keyed cache, the shared call budget
// and the shared concurrency slots.
func (m *meaningSearch) composeAnswer(ctx context.Context, q string, p []nlsearch.Passage) ([]nlsearch.Para, error) {
	composer, ok := m.rw.(answerComposer)
	if !ok {
		return nil, errors.New("written answers unavailable")
	}
	raw, _ := json.Marshal(struct {
		Q string
		P []nlsearch.Passage
	}{q, p})
	key := sha256.Sum256(raw)
	m.mu.Lock()
	entry, ok := m.composed[key]
	m.mu.Unlock()
	if ok && m.fresh(entry.at) {
		return entry.paras, nil
	}
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	default:
		return nil, errors.New("answer service busy")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	day := time.Now().UTC().Format("2006-01-02")
	if m.day != day {
		m.day, m.used = day, 0
	}
	if m.used >= m.limit {
		m.mu.Unlock()
		return nil, errors.New("daily API allowance reached")
	}
	m.used++
	m.mu.Unlock()
	paras, err := composer.Compose(ctx, q, p)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.composed == nil || !m.keep(len(m.composed)) {
		m.composed = make(map[[32]byte]composedEntry)
	}
	if m.keep(len(m.composed)) {
		m.composed[key] = composedEntry{paras, time.Now()}
	}
	m.mu.Unlock()
	return paras, nil
}

// aiAnswer serves POST /search/ai/: the answer box's content, as HTML.
// With empty=1 (the page's own search found nothing) it also returns the
// texts the meaning search found, for the page to show under the answer.
func (s *Server) aiAnswer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !sameSearchOrigin(r) {
		http.Error(w, "cross-site search is not allowed", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil {
		http.Error(w, "invalid search form", http.StatusBadRequest)
		return
	}
	q := strings.TrimSpace(r.PostForm.Get("q"))
	if q == "" || len([]rune(q)) > 500 {
		http.Error(w, "question must be 1-500 characters", http.StatusBadRequest)
		return
	}
	if !IsQuestion(q) {
		http.Error(w, "not a question", http.StatusBadRequest)
		return
	}
	view := aiView{PerHour: questionsPerHour}
	start := time.Now()
	ctx, usage := nlsearch.WithUsage(r.Context())
	var reason error // why the answer is unavailable, for the event
	render := func() {
		s.trackAI(r, q, view, usage, reason, time.Since(start))
		ts := s.pages
		if s.lang(r) == "ar" {
			ts = s.pagesAr
		}
		if err := ts["search"].ExecuteTemplate(w, "ai_answer", view); err != nil {
			s.captureError(r, "ai answer render", err)
		}
	}
	if s.meaning == nil {
		view.Unavailable, reason = true, errors.New("AI search is off")
		render()
		return
	}
	view.Provider = s.meaning.provider
	who := aiVisitor(w, r)
	ip := clientIP(r)
	wait, ok := s.meaning.visitors.Allow(who)
	if ok {
		if wait, ok = s.meaning.clients.Allow(ip); !ok {
			view.Network = true
		}
	}
	if !ok {
		view.Limited, view.WaitMin = true, max(1, int((wait+time.Minute-1)/time.Minute))
		view.Question = q
		render()
		return
	}
	view.Left = min(s.meaning.visitors.Left(who), s.meaning.clients.Left(ip))
	ctx, cancel := context.WithTimeout(ctx, 28*time.Second)
	defer cancel()
	res, err := s.SearchMeaning(ctx, q, s.meaning)
	if err == nil && r.PostForm.Get("empty") == "1" && found(res) {
		view.Results = &res
	}
	if err != nil {
		view.Unavailable, reason = true, err
		render()
		return
	}
	if res.Missing != "" {
		view.Missing = true
		render()
		return
	}
	sources, err := s.answerSources(ctx, q, res)
	if err != nil {
		view.Unavailable, reason = true, err
		render()
		return
	}
	if s.vector != nil {
		if extra, vectorErr := s.vectorSources(ctx, q); vectorErr == nil {
			sources = mergeAnswerSources(sources, extra)
		}
	}
	if len(sources) == 0 {
		render()
		return
	}
	passages := make([]nlsearch.Passage, 0, len(sources))
	byID := map[string]answerSource{}
	for _, src := range sources {
		p := src.Passage
		if strings.HasPrefix(src.Path, "/ruling/") {
			p.Label = "Court ruling (one case) · " + p.Label
		} else if src.Unofficial {
			p.Label = "Unofficial explanation · " + p.Label
		}
		passages = append(passages, p)
		byID[src.ID] = src
	}
	paras, err := s.meaning.composeAnswer(ctx, q, passages)
	if err != nil {
		view.Unavailable, reason = true, err
		render()
		return
	}
	view.Paras, view.Sources = numberSources(paras, byID)
	render()
}

// aiVisitor names the browser asking, by a random ll_ai cookie it is given
// on its first question, so one visitor's limit never touches another's.
// The cookie holds nothing but that random ID.
func aiVisitor(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie("ll_ai"); err == nil && len(c.Value) == 32 && strings.Trim(c.Value, "0123456789abcdef") == "" {
		return "v:" + c.Value
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{Name: "ll_ai", Value: id, Path: "/", MaxAge: 365 * 24 * 3600,
		HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteLaxMode})
	return "v:" + id
}

// Gemini 3.8 Flash, USD per million tokens (ai.google.dev pricing, 30 Oct
// 2026; it doubles on 1 Jan 2027). Only for the cost estimate in events.
const (
	aiUSDPerMIn  = 0.75
	aiUSDPerMOut = 3.75
)

// trackAI sends one ai_question event to PostHog for every question the box
// answers or declines: what happened, what it cost, and how long it took.
// The browser's PostHog ID (form field ph) ties it to the visitor's session.
func (s *Server) trackAI(r *http.Request, q string, v aiView, u *nlsearch.Usage, reason error, took time.Duration) {
	outcome := "answered"
	switch {
	case v.Limited:
		outcome = "limited"
	case v.Unavailable:
		outcome = "unavailable"
	case v.Missing:
		outcome = "missing_text"
	case len(v.Paras) == 0:
		outcome = "no_answer"
	}
	calls, in, out := u.Totals()
	quotes := 0
	for _, src := range v.Sources {
		quotes += len(src.Quotes)
	}
	props := map[string]any{
		"question":        cut(q, 300),
		"question_lang":   scriptOf(q),
		"page_lang":       s.lang(r),
		"outcome":         outcome,
		"model_calls":     calls,
		"tokens_in":       in,
		"tokens_out":      out,
		"cost_usd":        float64(in)*aiUSDPerMIn/1e6 + float64(out)*aiUSDPerMOut/1e6,
		"cached":          outcome == "answered" && calls == 0,
		"duration_ms":     took.Milliseconds(),
		"paragraphs":      len(v.Paras),
		"sources":         len(v.Sources),
		"quotes":          quotes,
		"questions_left":  v.Left,
		"wait_minutes":    v.WaitMin,
		"network_limited": v.Network,
		"brought_results": v.Results != nil,
		"bot":             IsBot(r),
	}
	if reason != nil {
		props["reason"] = cut(reason.Error(), 120)
	}
	if id := r.PostForm.Get("ph"); id != "" && len(id) <= 80 {
		props["distinct_id"] = id
	}
	s.captureEvent("ai_question", props)
}

// scriptOf names the main script of q: "arabic", "latin" or "other".
func scriptOf(q string) string {
	var ar, lat, other int
	for _, c := range q {
		switch {
		case unicode.Is(unicode.Arabic, c):
			ar++
		case unicode.Is(unicode.Latin, c):
			lat++
		case unicode.IsLetter(c):
			other++
		}
	}
	switch {
	case ar >= lat && ar >= other && ar > 0:
		return "arabic"
	case lat >= other && lat > 0:
		return "latin"
	case other > 0:
		return "other"
	}
	return "none"
}

// numberSources numbers the cited passages in the order the answer first
// cites them, and drops passages it does not cite. Two passages of the same
// page (its Arabic and French text) share one number. Each source keeps the
// quotes taken from it, once each.
func numberSources(paras []nlsearch.Para, byID map[string]answerSource) ([]aiPara, []aiSource) {
	var out []aiPara
	var sources []aiSource
	num := map[string]int{} // by page path
	quoted := map[string]bool{}
	for _, p := range paras {
		ap := aiPara{Text: p.Text}
		seen := map[int]bool{}
		for _, c := range p.Quotes {
			src, ok := byID[c.Source]
			if !ok {
				continue
			}
			n, ok := num[src.Path]
			if !ok {
				sources = append(sources, aiSource{answerSource: src})
				n = len(sources)
				num[src.Path] = n
			}
			if !quoted[src.Path+"\x00"+c.Quote] {
				quoted[src.Path+"\x00"+c.Quote] = true
				sources[n-1].Quotes = append(sources[n-1].Quotes, c.Quote)
			}
			if !seen[n] {
				seen[n] = true
				ap.Refs = append(ap.Refs, n)
			}
		}
		out = append(out, ap)
	}
	return out, sources
}
