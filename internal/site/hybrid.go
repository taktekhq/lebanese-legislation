package site

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
	"github.com/taktekhq/lebanese-legislation/internal/semantic"
)

type vectorSearcher interface {
	Search(context.Context, string, int) ([]semantic.Hit, error)
}
type vectorEntry struct {
	hits []semantic.Hit
	at   time.Time
}

func (s *Server) SetVectorSearch(v vectorSearcher) { s.vector = v }

func (m *meaningSearch) vectorHits(ctx context.Context, q string, v vectorSearcher) ([]semantic.Hit, error) {
	key := sha256.Sum256([]byte(q))
	m.mu.Lock()
	entry, ok := m.vectors[key]
	m.mu.Unlock()
	if ok && m.fresh(entry.at) {
		return entry.hits, nil
	}
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	default:
		return nil, errors.New("semantic search busy")
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
	hits, err := v.Search(ctx, q, 48)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.vectors == nil || !m.keep(len(m.vectors)) {
		m.vectors = make(map[[32]byte]vectorEntry)
	}
	if m.keep(len(m.vectors)) {
		m.vectors[key] = vectorEntry{hits, time.Now()}
	}
	m.mu.Unlock()
	return hits, nil
}

func (s *Server) vectorSources(ctx context.Context, q string) ([]answerSource, error) {
	if s.vector == nil || s.meaning == nil {
		return nil, nil
	}
	t, _ := tidyQuery(q)
	if t == "" {
		t = q
	}
	rq := readQuery(t)
	if rq.lawCite() || rq.rulingCite() {
		return nil, nil
	}
	if _, _, ok := parseArticleRef(t); ok {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if s.exactTitle(ctx, t) != nil {
		return nil, nil
	}
	hits, err := s.meaning.vectorHits(ctx, q, s.vector)
	if err != nil {
		return nil, err
	}
	out := []answerSource{}
	seen := map[string]int{}
	canonical := map[string]semantic.Document{}
	invalid := map[string]bool{}
	for _, h := range hits {

		key := fmt.Sprintf("%s/%d/%s/%s", h.Kind, h.ID, h.Part, h.Lang)
		if invalid[key] {
			continue
		}
		d, exists := canonical[key]
		if !exists {
			if len(canonical)+len(invalid) >= 12 {
				continue
			}
			var e error
			d, e = semantic.Load(ctx, s.db, h.Document)
			if e != nil {
				invalid[key] = true
				continue
			}
			canonical[key] = d
		}
		if semantic.Hash(d.Text) != h.Hash {
			continue
		}
		body := []rune(d.Text)
		if h.Start < 0 || h.End <= h.Start || h.End > len(body) || h.End-h.Start > 900 {
			continue
		}
		excerpt := string(body[h.Start:h.End])
		if excerpt != h.Text {
			continue
		}
		path, unofficial := vectorPath(d)
		if path == "" || seen[path+"|"+d.Lang] >= 2 {
			continue
		}
		seen[path+"|"+d.Lang]++
		label := d.Title
		switch d.Kind {
		case "ruling":
			label = "حكم قضائي: " + label
		case "opinion":
			label = "رأي استشاري: " + label
		case "doctrine":
			label = "مقالة فقهية: " + label
		}
		out = append(out, answerSource{Passage: nlsearch.Passage{ID: fmt.Sprintf("v%d", len(out)+1), Text: excerpt, Label: label}, Path: path, Title: label, Lang: d.Lang, Unofficial: unofficial})
		if len(out) == 16 {
			break
		}
	}
	return out, nil
}

func vectorPath(d semantic.Document) (string, bool) {
	switch d.Kind {
	case "article":
		return artPath(d.ID, d.Part), false
	case "law":
		return lawPath(d.ID), d.Lang == "en"
	case "topic":
		prefix := ""
		if d.Lang == "en" {
			prefix = "/en"
		}
		return prefix + "/topic/" + d.Part + "/", true
	case "ruling", "opinion", "agreement", "research", "doctrine":
		return "/" + d.Kind + "/" + strconv.Itoa(d.ID) + "/", false
	}
	return "", false
}

func mergeAnswerSources(a, b []answerSource) []answerSource {
	out := []answerSource{}
	seen := map[string]bool{}
	for i := 0; i < max(len(a), len(b)); i++ {
		for _, set := range [][]answerSource{a, b} {
			if i >= len(set) || len(out) >= 16 {
				continue
			}
			p := set[i]
			key := p.Path + "|" + p.Lang + "|" + strings.TrimSpace(p.Text)
			if seen[key] {
				continue
			}
			seen[key] = true
			p.ID = fmt.Sprintf("s%d", len(out)+1)
			out = append(out, p)
		}
	}
	return out
}
