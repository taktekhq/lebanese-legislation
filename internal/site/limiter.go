package site

// Overload protection for a small shared box: a per-client token bucket, a
// global semaphore on concurrent DB work, and a short-lived cache for
// identical tool calls. Shared by /mcp (mcp.go) and /api/v1 (api.go).

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"
)

// ---------------------------------------------------------------- client IP

// clientIP is who to blame for a request: Cloudflare's CF-Connecting-IP
// when present (the reverse proxy forwards it unchanged),
// else the connection's own remote address.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// ---------------------------------------------------------------- token bucket

type bucket struct {
	tokens float64
	last   time.Time
}

// ipLimiter is one token bucket per client IP: ratePerMin tokens added per
// minute, up to burst. Idle buckets are swept so the map does not grow
// without bound on a public endpoint.
type ipLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*bucket
	ratePerSec float64
	burst      float64
	lastSweep  time.Time
}

// newHourLimiter allows perHour calls an hour per client IP, all of them at
// once if the client has been quiet for the hour.
func newHourLimiter(perHour int) *ipLimiter {
	return &ipLimiter{buckets: map[string]*bucket{}, ratePerSec: float64(perHour) / 3600, burst: float64(perHour), lastSweep: time.Now()}
}

func newIPLimiter(ratePerMin, burst int) *ipLimiter {
	return &ipLimiter{buckets: map[string]*bucket{}, ratePerSec: float64(ratePerMin) / 60, burst: float64(burst), lastSweep: time.Now()}
}

// SetRateLimit replaces the per-client limit shared by /api/v1 and /mcp:
// perMinute requests a minute per client IP, up to burst at once. 0 turns
// the limit off. Call before serving.
func (s *Server) SetRateLimit(perMinute, burst int) {
	s.ipLim = newIPLimiter(perMinute, burst)
}

// Allow reports whether ip may make one more call now; when not, it says
// how long until the next token is available (for Retry-After). A limiter
// with no rate lets every call through.
func (l *ipLimiter) Allow(ip string) (time.Duration, bool) {
	if l.ratePerSec <= 0 {
		return 0, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.buckets[ip]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[ip] = b
	} else {
		b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.ratePerSec)
		b.last = now
	}
	l.sweep(now)
	if b.tokens < 1 {
		wait := time.Duration((1 - b.tokens) / l.ratePerSec * float64(time.Second))
		return wait, false
	}
	b.tokens--
	return 0, true
}

// Left says how many whole calls ip may still make now, without using one.
func (l *ipLimiter) Left(ip string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[ip]
	if b == nil {
		return int(l.burst)
	}
	return int(min(l.burst, b.tokens+time.Since(b.last).Seconds()*l.ratePerSec))
}

// sweep drops buckets idle for 10 minutes, at most once a minute, so a
// public endpoint under many distinct IPs does not leak memory.
func (l *ipLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	// A bucket idle long enough to be full again is the same as no bucket;
	// dropping it sooner would hand out a fresh allowance early.
	idle := 10 * time.Minute
	if full := time.Duration(l.burst / l.ratePerSec * float64(time.Second)); full > idle {
		idle = full
	}
	for ip, b := range l.buckets {
		if now.Sub(b.last) > idle {
			delete(l.buckets, ip)
		}
	}
}

// ---------------------------------------------------------------- global semaphore

// dbSemaphore caps concurrent DB-touching requests across /mcp and, once
// wired, /api/v1, so a burst of calls queues briefly instead of saturating
// Postgres on a 2 vCPU box.
type dbSemaphore struct{ slots chan struct{} }

func newDBSemaphore(n int) *dbSemaphore { return &dbSemaphore{slots: make(chan struct{}, n)} }

// acquire blocks up to wait for a free slot; release must be called
// exactly once when the caller is done. ok is false when no slot opened up
// in time (the caller should answer 503).
func (d *dbSemaphore) acquire(ctx context.Context, wait time.Duration) (release func(), ok bool) {
	select {
	case d.slots <- struct{}{}:
		return func() { <-d.slots }, true
	default:
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case d.slots <- struct{}{}:
		return func() { <-d.slots }, true
	case <-timer.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

// ---------------------------------------------------------------- short-lived cache

type cacheEntry struct {
	value   any
	expires time.Time
}

// ttlCache holds the result of an identical tool call (same tool name,
// same arguments) for a short time, so a repeated search or lookup (a
// model retrying, or several users asking the same thing) does not hit
// Postgres again. FIFO eviction once max is reached: simple, and plenty
// for the handful of distinct calls this gets in any one minute.
type ttlCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	order   []string
	max     int
	ttl     time.Duration
}

func newTTLCache(max int, ttl time.Duration) *ttlCache {
	return &ttlCache{entries: map[string]cacheEntry{}, max: max, ttl: ttl}
}

func cacheKey(tool string, args json.RawMessage) string {
	// args is already canonical JSON as unmarshalled from the wire for a
	// given call; two calls with the same bytes get the same key, which is
	// all that matters (not a sorted-key canonicalization).
	return tool + "\x00" + string(args)
}

func (c *ttlCache) get(tool string, args json.RawMessage) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[cacheKey(tool, args)]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.value, true
}

func (c *ttlCache) set(tool string, args json.RawMessage, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := cacheKey(tool, args)
	if _, exists := c.entries[k]; !exists {
		if len(c.order) >= c.max {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
		c.order = append(c.order, k)
	}
	c.entries[k] = cacheEntry{value: value, expires: time.Now().Add(c.ttl)}
}
