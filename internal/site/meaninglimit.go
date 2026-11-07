package site

import (
	"context"
	"crypto/sha256"
	"errors"
	"log"
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
	clients     *ipLimiter   // per IP, for the quote box (answer.go)
	devices     dailyCounter // AI questions per device a day (ai.go)
	networks    dailyCounter // AI questions per IP a day, for bans
	ipUnits     dailyCounter // allowance units each IP spent today, on any path
	ipDaily     int          // units one IP may spend a day
	bans        banList
	selected    map[[32]byte]selectedEntry
	composed    map[[32]byte]composedEntry
	vectors     map[[32]byte]vectorEntry

	// How many answers each cache keeps, and for how long. 0 turns caching off.
	cacheSize int
	cacheTTL  time.Duration
}

// Defaults when SetMeaningLimits is not called: two provider calls at once,
// and up to 1,000 answers per cache, each kept for an hour.

const (
	defaultMeaningConcurrency = 2
	defaultMeaningCacheSize   = 1000
	defaultMeaningCacheTTL    = time.Hour
)

// DefaultIPDailyUnits is how many units of the site-wide allowance one IP
// may spend a day, on every path together (answer box, quote box, API).
// A device's own limit, 10 quick answers (about 3 units each) and one deep
// answer (up to 18), costs about 50; this leaves room for a few devices on
// one network and for the quote box and API on top.
const DefaultIPDailyUnits = 150

var (
	errAllowance = errors.New("daily API allowance reached")
	errIPDaily   = errors.New("daily AI allowance for this address reached")
	errIPBanned  = errors.New("AI answers blocked for this address")
)

type spenderKey struct{}

// withSpender names the client IP that pays for AI calls made under ctx.
func withSpender(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, spenderKey{}, ip)
}

// spender is the IP named by withSpender. Calls without one are charged to
// one shared key, so an untagged path can never spend without a cap.
func spender(ctx context.Context) string {
	ip, _ := ctx.Value(spenderKey{}).(string)
	return ip
}

// take spends n units of today's site-wide allowance for the IP that ctx
// names. It checks the ban list and that IP's daily units first, and spends
// nothing when either check or the site-wide allowance says no.
func (m *meaningSearch) take(ctx context.Context, n int) error {
	ip := spender(ctx)
	if m.bans.banned(ip) {
		return errIPBanned
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if day := beirutDay(time.Now()); m.day != day {
		m.day, m.used = day, 0
	}
	if m.used+n > m.limit {
		return errAllowance
	}
	if !m.ipUnits.add(ip, n, m.ipDaily) {
		return errIPDaily
	}
	m.used += n
	return nil
}

// ipHas says whether ip may still spend n units today.
func (m *meaningSearch) ipHas(ip string, n int) bool {
	return m.ipDaily <= 0 || m.ipUnits.count(ip)+n <= m.ipDaily
}

// SetMeaningIPDaily sets how many allowance units one IP may spend a day
// (DefaultIPDailyUnits when not called). Call after SetMeaningSearch.
func (s *Server) SetMeaningIPDaily(units int) {
	if s.meaning == nil || units < 1 {
		return
	}
	s.meaning.ipDaily = units
}

// fresh says whether a cached answer from at may still be used.
// budgetFor says whether today's site-wide allowance still has n calls.
func (m *meaningSearch) budgetFor(n int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if day := beirutDay(time.Now()); m.day != day {
		m.day, m.used = day, 0
	}
	return m.used+n <= m.limit
}

func (m *meaningSearch) fresh(at time.Time) bool {
	return m.cacheTTL > 0 && time.Since(at) < m.cacheTTL
}

// keep says whether a cache holding n answers may take one more.
func (m *meaningSearch) keep(n int) bool { return m.cacheSize > 0 && m.cacheTTL > 0 && n < m.cacheSize }

// SetMeaningLimits sets how many provider calls run at once and how the
// answer caches behave. Call it after SetMeaningSearch, before serving.
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
		ipDaily: DefaultIPDailyUnits, cacheSize: defaultMeaningCacheSize, cacheTTL: defaultMeaningCacheTTL}
}

// SetMeaningSearch enables explicit, user-requested interpretation. Call before serving.
// The allowance counts attempted API calls per UTC day, in this server process.
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
	if err := m.take(ctx, 1); err != nil {
		return nlsearch.Plan{}, err
	}
	p, err := m.rw.Rewrite(ctx, q)
	if err == nil {
		log.Printf("ai rewrite: %d tokens in, %d out", p.InputTokens, p.OutputTokens)
		nlsearch.AddUsage(ctx, p.InputTokens, p.OutputTokens)
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
