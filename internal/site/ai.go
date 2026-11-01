package site

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

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

type aiView struct {
	Paras                []aiPara
	Sources              []answerSource
	Provider             string
	Unavailable, Missing bool
	Results              *Results // what the meaning search found, when the page found nothing
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
	view := aiView{}
	render := func() {
		ts := s.pages
		if s.lang(r) == "ar" {
			ts = s.pagesAr
		}
		if err := ts["search"].ExecuteTemplate(w, "ai_answer", view); err != nil {
			s.captureError(r, "ai answer render", err)
		}
	}
	if s.meaning == nil {
		view.Unavailable = true
		render()
		return
	}
	view.Provider = s.meaning.provider
	if _, ok := s.meaning.clients.Allow(clientIP(r)); !ok {
		view.Unavailable = true
		render()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 28*time.Second)
	defer cancel()
	res, err := s.SearchMeaning(ctx, q, s.meaning)
	if err == nil && r.PostForm.Get("empty") == "1" && found(res) {
		view.Results = &res
	}
	if err != nil {
		view.Unavailable = true
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
		view.Unavailable = true
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
		view.Unavailable = true
		render()
		return
	}
	view.Paras, view.Sources = numberSources(paras, byID)
	render()
}

// numberSources numbers the cited passages in the order the answer first
// cites them, and drops passages it does not cite. Two passages of the same
// page (its Arabic and French text) share one number.
func numberSources(paras []nlsearch.Para, byID map[string]answerSource) ([]aiPara, []answerSource) {
	var out []aiPara
	var sources []answerSource
	num := map[string]int{} // by page path
	for _, p := range paras {
		ap := aiPara{Text: p.Text}
		seen := map[int]bool{}
		for _, id := range p.Sources {
			src, ok := byID[id]
			if !ok {
				continue
			}
			n, ok := num[src.Path]
			if !ok {
				sources = append(sources, src)
				n = len(sources)
				num[src.Path] = n
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
