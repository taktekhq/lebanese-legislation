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
	// cacheSize and cacheTTL bound each of the three caches; zero turns caching off.
	cacheSize int
	cacheTTL  time.Duration
}

// Defaults, as the Python service had: two provider calls at once, 256 cached answers for an hour.
const (
	defaultMeaningConcurrency = 2
	defaultMeaningCacheSize   = 256
	defaultMeaningCacheTTL    = time.Hour
)

func (m *meaningSearch) fresh(at time.Time) bool {
	return m.cacheTTL > 0 && time.Since(at) < m.cacheTTL
}

// keep reports whether a cache holding n entries can take one more, and false when caching is off.
func (m *meaningSearch) keep(n int) bool { return m.cacheSize > 0 && m.cacheTTL > 0 && n < m.cacheSize }

// SetMeaningLimits sets how many provider calls run at once and how much is cached.
func (s *Server) SetMeaningLimits(concurrency, cacheSize int, cacheTTL time.Duration) {
	if s.meaning == nil || concurrency < 1 {
		return
	}
	s.meaning.slots = make(chan struct{}, concurrency)
	s.meaning.cacheSize, s.meaning.cacheTTL = cacheSize, cacheTTL
}

func newMeaningSearch(rw QueryRewriter, provider string, shares bool, limit int) *meaningSearch {
	return &meaningSearch{rw: rw, provider: provider, shares: shares, limit: limit, cache: make(map[[32]byte]meaningEntry),
		slots: make(chan struct{}, defaultMeaningConcurrency), clients: newIPLimiter(6, 2),
		cacheSize: defaultMeaningCacheSize, cacheTTL: defaultMeaningCacheTTL}
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
		return v.plan, ok && m.fresh(v.at)
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
		if !m.keep(len(m.cache)) {
			clear(m.cache)
		}
		if m.keep(len(m.cache)) {
			m.cache[key] = meaningEntry{p, time.Now()}
		}
		m.mu.Unlock()
	}
	return p, err
}
