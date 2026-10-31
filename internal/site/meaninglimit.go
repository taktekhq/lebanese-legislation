package site

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
)

type meaningEntry struct {
	plan nlsearch.Plan
	at   time.Time
}
type meaningSearch struct {
	rw          QueryRewriter
	provider    string
	shares      bool
	limit, used int
	day         string
	mu          sync.Mutex
	cache       map[[32]byte]meaningEntry
	slots       chan struct{}
	clients     *ipLimiter
	selected    map[[32]byte]selectedEntry
	vectors     map[[32]byte]vectorEntry
}

func newMeaningSearch(rw QueryRewriter, provider string, shares bool, limit int) *meaningSearch {
	return &meaningSearch{rw: rw, provider: provider, shares: shares, limit: limit, cache: make(map[[32]byte]meaningEntry), slots: make(chan struct{}, 2), clients: newIPLimiter(6, 2)}
}

func (s *Server) SetMeaningSearch(rw QueryRewriter, provider string, shares bool, daily int) {
	if rw == nil || daily < 1 || daily > 10000 {
		return
	}
	s.meaning = newMeaningSearch(rw, provider, shares, daily)
}

func (m *meaningSearch) Rewrite(ctx context.Context, q string) (nlsearch.Plan, error) {
	if err := ctx.Err(); err != nil {
		return nlsearch.Plan{}, err
	}
	key := sha256.Sum256([]byte(q))
	cached := func() (nlsearch.Plan, bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		v, ok := m.cache[key]
		return v.plan, ok && time.Since(v.at) < time.Hour
	}
	if p, ok := cached(); ok {
		return p, nil
	}
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	default:
		return nlsearch.Plan{}, errors.New("AI search busy")
	}
	if p, ok := cached(); ok {
		return p, nil
	}
	m.mu.Lock()
	day := time.Now().UTC().Format("2006-01-02")
	if m.day != day {
		m.day, m.used = day, 0
	}
	if m.used >= m.limit {
		m.mu.Unlock()
		return nlsearch.Plan{}, errors.New("AI search daily allowance reached")
	}
	m.used++
	m.mu.Unlock()
	p, err := m.rw.Rewrite(ctx, q)
	if err == nil {
		m.mu.Lock()
		if len(m.cache) >= 1000 {
			clear(m.cache)
		}
		m.cache[key] = meaningEntry{p, time.Now()}
		m.mu.Unlock()
	}
	return p, err
}
