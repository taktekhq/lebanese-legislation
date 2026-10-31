package site

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
	"github.com/taktekhq/lebanese-legislation/internal/semantic"
	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

type passageSelector interface {
	Select(context.Context, string, []nlsearch.Passage) ([]nlsearch.Quote, error)
}
type answerSource struct {
	nlsearch.Passage
	Path, Title, Lang, Status string
	Unofficial                bool
}
type answerQuote struct {
	Quote  string
	Source answerSource
}
type answerView struct {
	Quotes               []answerQuote
	Unavailable, Missing bool
}
type selectedEntry struct {
	quotes []nlsearch.Quote
	at     time.Time
}

func (m *meaningSearch) selectEvidence(ctx context.Context, q string, p []nlsearch.Passage) ([]nlsearch.Quote, error) {
	selector, ok := m.rw.(passageSelector)
	if !ok {
		return nil, errors.New("evidence selection unavailable")
	}
	raw, _ := json.Marshal(struct {
		Q string
		P []nlsearch.Passage
	}{q, p})
	key := sha256.Sum256(raw)
	m.mu.Lock()
	entry, ok := m.selected[key]
	m.mu.Unlock()
	if ok && m.fresh(entry.at) {
		return entry.quotes, nil
	}
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	default:
		return nil, errors.New("evidence service busy")
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
	qs, err := selector.Select(ctx, q, p)
	if err != nil {
		return nil, err
	}
	qs, err = nlsearch.ValidateQuotes(qs, p)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.selected == nil || !m.keep(len(m.selected)) {
		m.selected = make(map[[32]byte]selectedEntry)
	}
	if m.keep(len(m.selected)) {
		m.selected[key] = selectedEntry{qs, time.Now()}
	}
	m.mu.Unlock()
	return qs, nil
}

func sameSearchOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		return err == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
	}
	return true
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
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
	view := answerView{}
	render := func() {
		ts := s.pages
		if s.lang(r) == "ar" {
			ts = s.pagesAr
		}
		if err := ts["search"].ExecuteTemplate(w, "answer", view); err != nil {
			s.captureError(r, "answer render", err)
		}
	}
	if s.meaning == nil {
		view.Unavailable = true
		render()
		return
	}
	if _, ok := s.meaning.clients.Allow(clientIP(r)); !ok {
		view.Unavailable = true
		render()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	res, err := s.SearchMeaning(ctx, q, s.meaning)
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
		passages = append(passages, src.Passage)
		byID[src.ID] = src
	}
	qs, err := s.meaning.selectEvidence(ctx, q, passages)
	if err != nil {
		view.Unavailable = true
		render()
		return
	}
	for _, q := range qs {
		view.Quotes = append(view.Quotes, answerQuote{q.Quote, byID[q.SourceID]})
	}
	render()
}

func (s *Server) answerArticle(ctx context.Context, law int, number string) (ar, fr, title, status string, err error) {
	d, e := semantic.Load(ctx, s.db, semantic.Document{Kind: "article", ID: law, Part: number, Lang: "ar"})
	if e != nil {
		err = e
		return
	}
	ar, title = d.Text, d.Title
	if f, e := semantic.Load(ctx, s.db, semantic.Document{Kind: "article", ID: law, Part: number, Lang: "fr"}); e == nil {
		fr = f.Text
	}
	err = s.db.QueryRow(ctx, `SELECT status FROM legislation WHERE id=$1`, law).Scan(&status)
	return
}

func textWindow(text, q string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	start := 0

	for _, term := range strings.Fields(q) {
		if len([]rune(term)) < 3 {
			continue
		}
		if i := strings.Index(strings.ToLower(text), strings.ToLower(term)); i >= 0 {
			start = maxInt(0, len([]rune(text[:i]))-300)
			break
		}
	}
	if start+max > len(runes) {
		start = len(runes) - max
	}
	return string(runes[start : start+max])
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Server) answerSources(ctx context.Context, q string, res Results) ([]answerSource, error) {
	var out []answerSource
	seen := map[string]bool{}
	add := func(text, path, title, lang, status string, unofficial bool) {
		if strings.TrimSpace(text) == "" || len(out) >= 16 || seen[path+"|"+lang+"|"+text] {
			return
		}
		seen[path+"|"+lang+"|"+text] = true
		text = textWindow(text, q+" "+res.Via, 900)
		id := fmt.Sprintf("s%d", len(out)+1)
		out = append(out, answerSource{Passage: nlsearch.Passage{ID: id, Text: text, Label: title}, Path: path, Title: title, Lang: lang, Status: status, Unofficial: unofficial})
	}

	for _, r := range res.Rulings[:min(4, len(res.Rulings))] {
		var text string
		if s.db.QueryRow(ctx, `SELECT coalesce(text_ar,'') FROM ruling WHERE id=$1`, r.ID).Scan(&text) == nil {
			add(text, fmt.Sprintf("/ruling/%d/", r.ID), "حكم قضائي: "+r.Title, "ar", "", false)
		}
	}
	type articleBody struct {
		ar, fr, title, status string
		err                   error
	}
	cache := map[string]articleBody{}
	load := func(id int, num string) articleBody {
		key := fmt.Sprintf("%d/%s", id, num)
		if v, ok := cache[key]; ok {
			return v
		}
		a, b, c, d, e := s.answerArticle(ctx, id, num)
		v := articleBody{a, b, c, d, e}
		cache[key] = v
		return v
	}
	laws := map[int]bool{}
	for _, l := range res.Laws {
		laws[l.ID] = true
	}
	for _, a := range res.Articles {
		laws[a.ID] = true
	}
	for _, a := range res.Articles[:min(6, len(res.Articles))] {
		b := load(a.ID, a.Article)
		if b.err != nil {
			continue
		}
		label := "المادة " + a.Article + " من " + b.title
		add(b.ar, artPath(a.ID, a.Article), label, "ar", b.status, false)
		if b.fr != "" {
			add(b.fr, artPath(a.ID, a.Article), label, "fr", b.status, false)
		}
	}

	type candidate struct {
		t     *topics.Topic
		p     topics.Para
		lang  string
		score int
	}
	var candidates []candidate
	terms := strings.Fields(topicFold(q + " " + res.Via))
	for _, t := range topics.All() {
		relevant := false
		for _, c := range t.Articles {
			if laws[c.Law] {
				relevant = true
				break
			}
		}
		if !relevant {
			for _, h := range res.Topics {
				if h.Slug == t.Slug {
					relevant = true
					break
				}
			}
		}
		if !relevant {
			continue
		}
		for _, lang := range []string{"en", "ar"} {
			c := t.Lang(lang)
			paras := []topics.Para{c.Answer}
			for _, sec := range c.Sections {
				paras = append(paras, sec.Paras...)
			}
			for _, p := range paras {
				if p.Note || len(p.Cites) == 0 || len(p.Cites) > 4 {
					continue
				}
				fold := " " + topicFold(p.Text) + " "
				score := 0
				for _, term := range terms {
					if !topicStop[term] && strings.Contains(fold, " "+term+" ") {
						score++
					}
				}
				if score > 0 {
					candidates = append(candidates, candidate{t, p, lang, score})
				}
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	accepted := 0
	for _, c := range candidates[:min(8, len(candidates))] {
		valid := true
		for _, id := range c.p.Cites {
			cite := c.t.Cite(id)
			if cite == nil {
				valid = false
				break
			}
			body := load(cite.Law, cite.Article)
			if body.err != nil || !strings.Contains(body.ar, cite.Quote) {
				valid = false
				break
			}
		}
		if valid {
			prefix := ""
			if c.lang == "en" {
				prefix = "/en"
			}
			add(c.p.Text, prefix+"/topic/"+c.t.Slug+"/", c.t.Lang(c.lang).Title, c.lang, "", true)
			accepted++
			if accepted == 4 {
				break
			}
		}
	}
	if len(out) == 0 && len(res.Laws) > 0 {

		rows, err := s.db.Query(ctx, `SELECT number FROM legislation_article WHERE law_id=$1 ORDER BY position LIMIT 2`, res.Laws[0].ID)
		if err != nil {
			return nil, err
		}
		var nums []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return nil, err
			}
			nums = append(nums, n)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, n := range nums {
			b := load(res.Laws[0].ID, n)
			if b.err == nil {
				add(b.ar, artPath(res.Laws[0].ID, n), b.title+" · "+n, "ar", b.status, false)
			}
		}
	}
	return out, nil
}
