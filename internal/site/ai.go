package site

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
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
	ComposeWith(context.Context, string, []nlsearch.Passage, nlsearch.Mode) ([]nlsearch.Para, error)
	Expand(ctx context.Context, q string, searched, titles []string) (nlsearch.Plan, error)
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
	Results              *Results // what the meaning search found, when the page found nothing
	Left, PerDay         int      // questions this device may still ask today, of PerDay
	Limited              bool     // this device used its questions (or its deep answer) for today
	SiteFull             bool     // the whole site used today's AI allowance
	Banned               bool     // this IP asked far more than anyone could by hand
	WaitH, WaitM         int      // when Limited or SiteFull: time until midnight in Beirut
	Question, IP         string   // for the "email us" links
	Deep                 bool     // this is a deep answer
	DeepLeft             int      // deep answers this device may still ask today
}

// Budget weights, in model calls of the daily allowance: a deep answer
// reads about ten times the text of a quick one, so it counts that much more.
const (
	quickWeight = 1
	deepWeight  = 12
)

// spend takes n calls from today's site-wide allowance.
func (m *meaningSearch) spend(n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if day := beirutDay(time.Now()); m.day != day {
		m.day, m.used = day, 0
	}
	if m.used+n > m.limit {
		return errors.New("daily API allowance reached")
	}
	m.used += n
	return nil
}

// composeAnswer is ComposeWith with a content-keyed cache, the shared call
// budget and the shared concurrency slots.
func (m *meaningSearch) composeAnswer(ctx context.Context, q string, p []nlsearch.Passage, mode nlsearch.Mode) ([]nlsearch.Para, error) {
	composer, ok := m.rw.(answerComposer)
	if !ok {
		return nil, errors.New("written answers unavailable")
	}
	raw, _ := json.Marshal(struct {
		Q, Mode string
		P       []nlsearch.Passage
	}{q, mode.Name, p})
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
	weight := quickWeight
	if mode.Name == nlsearch.Deep.Name {
		weight = deepWeight
	}
	if err := m.spend(weight); err != nil {
		return nil, err
	}
	paras, err := composer.ComposeWith(ctx, q, p, mode)
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

// expand asks the model for more search phrases, within the budget.
func (m *meaningSearch) expand(ctx context.Context, q string, searched, titles []string) ([]string, error) {
	composer, ok := m.rw.(answerComposer)
	if !ok {
		return nil, errors.New("deep answers unavailable")
	}
	if err := m.spend(1); err != nil {
		return nil, err
	}
	p, err := composer.Expand(ctx, q, searched, titles)
	return p.Queries, err
}

// aiAnswer serves POST /search/ai/: the answer box's content, as HTML.
// With empty=1 (the page's own search found nothing) it also returns the
// texts the meaning search found, for the page to show under the answer.
func (s *Server) aiAnswer(w http.ResponseWriter, r *http.Request) { s.serveAI(w, r, false) }

// aiDeepAnswer serves POST /search/ai/deep/: the longer answer a visitor asks
// for by button, one a day per device.
func (s *Server) aiDeepAnswer(w http.ResponseWriter, r *http.Request) { s.serveAI(w, r, true) }

func (s *Server) serveAI(w http.ResponseWriter, r *http.Request, deep bool) {
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
	view := aiView{PerDay: questionsPerDay, Deep: deep}
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
	who, ip := aiVisitor(w, r), clientIP(r)
	view.Question, view.IP = q, ip
	wait := untilMidnight(time.Now())
	view.WaitH, view.WaitM = int(wait/time.Hour), int(wait%time.Hour/time.Minute)
	// Misbehaving: 100 questions in a day from one IP. With 10 a device,
	// that is ten devices maxed out on one network, or a script.
	if s.meaning.bans.banned(ip) {
		view.Banned = true
		render()
		return
	}
	if n, _ := s.meaning.networks.take(ip, 0); n >= banPerDay {
		s.meaning.bans.ban(ip)
		log.Printf("ai answers: banned %s for %v after %d questions today", ip, banFor, n)
		s.captureEvent("ai_ip_banned", map[string]any{"questions_today": n, "ban_days": int(banFor / (24 * time.Hour))})
		view.Banned = true
		render()
		return
	}
	// Say so honestly when the day's budget is out.
	need := 2 * quickWeight
	if deep {
		need = deepWeight + 3
	}
	if !s.meaning.budgetFor(need) {
		view.SiteFull = true
		render()
		return
	}
	if deep {
		if _, ok := s.meaning.devices.take("deep:"+who, deepPerDay); !ok {
			view.Limited = true
			render()
			return
		}
	} else {
		used, ok := s.meaning.devices.take(who, questionsPerDay)
		if !ok {
			view.Limited = true
			render()
			return
		}
		view.Left = questionsPerDay - used
		view.DeepLeft = deepPerDay - s.meaning.devices.count("deep:"+who)
	}
	timeout := 28 * time.Second
	if deep {
		timeout = 75 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := s.SearchMeaning(ctx, q, s.meaning)
	if err == nil && !deep && r.PostForm.Get("empty") == "1" && found(res) {
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
	terms, err := s.answerSources(ctx, q, res)
	if err != nil {
		view.Unavailable, reason = true, err
		render()
		return
	}
	var vec []answerSource
	if s.vector != nil {
		if v, vectorErr := s.vectorSources(ctx, q); vectorErr == nil {
			vec = v
		}
	}
	mode, limit := nlsearch.Quick, nlsearch.Quick.Sources
	if deep {
		mode, limit = nlsearch.Deep, nlsearch.Deep.Sources
		moreVec, moreTerms := s.deepSources(ctx, q, res, append(append([]answerSource{}, vec...), terms...))
		vec, terms = append(vec, moreVec...), append(terms, moreTerms...)
	}
	sources := vectorFirst(vec, terms, limit)
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
	paras, err := s.meaning.composeAnswer(ctx, q, passages, mode)
	if err != nil {
		view.Unavailable, reason = true, err
		render()
		return
	}
	view.Paras, view.Sources = numberSources(paras, byID)
	render()
}

// deepSources runs the extra searches of a deep answer: the model names up to
// three more Arabic phrases (conditions, exceptions, procedure, rulings), and
// each runs through both searches, terms and vectors. A failed step only
// means fewer passages.
func (s *Server) deepSources(ctx context.Context, q string, res Results, have []answerSource) (vec, terms []answerSource) {
	var searched, titles []string
	if res.Via != "" {
		searched = strings.Split(res.Via, " / ")
	}
	seen := map[string]bool{}
	for _, h := range have {
		if !seen[h.Title] && len(titles) < 60 {
			seen[h.Title] = true
			titles = append(titles, cut(h.Title, 120))
		}
	}
	phrases, err := s.meaning.expand(ctx, q, searched, titles)
	if err != nil {
		return nil, nil
	}
	for _, p := range phrases {
		if r, err := s.SearchDocuments(ctx, p); err == nil {
			if t, err := s.answerSources(ctx, p, r); err == nil {
				terms = append(terms, t...)
			}
		}
		if s.vector != nil {
			if v, err := s.vectorSources(ctx, p); err == nil {
				vec = append(vec, v...)
			}
		}
	}
	return vec, terms
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

// Gemini 3.8 Flash and gemini-embedding-001, USD per million tokens
// (ai.google.dev pricing, 30 Oct 2026; Flash doubles on 1 Jan 2027). Only for
// the cost estimate in events; the rewrite runs on a cheaper Flash Lite, so
// the estimate is on the high side.
const (
	aiUSDPerMIn    = 0.75
	aiUSDPerMOut   = 3.75
	aiUSDPerMEmbed = 0.15
)

// trackAI sends one ai_question event to PostHog for every question the box
// answers or declines: what happened, what it cost, and how long it took.
// The browser's PostHog ID (form field ph) ties it to the visitor's session.
func (s *Server) trackAI(r *http.Request, q string, v aiView, u *nlsearch.Usage, reason error, took time.Duration) {
	outcome := "answered"
	switch {
	case v.Banned:
		outcome = "banned"
	case v.SiteFull:
		outcome = "site_full"
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
	embeds, embedTokens := u.Embeddings()
	mode := "quick"
	if v.Deep {
		mode = "deep"
	}
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
		"embedding_calls": embeds,
		"cost_usd":        float64(in)*aiUSDPerMIn/1e6 + float64(out)*aiUSDPerMOut/1e6 + float64(embedTokens)*aiUSDPerMEmbed/1e6,
		"cached":          outcome == "answered" && calls == 0,
		"mode":            mode,
		"deep_left":       v.DeepLeft,
		"duration_ms":     took.Milliseconds(),
		"paragraphs":      len(v.Paras),
		"sources":         len(v.Sources),
		"quotes":          quotes,
		"questions_left":  v.Left,
		"wait_minutes":    v.WaitH*60 + v.WaitM,
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
