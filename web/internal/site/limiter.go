package site

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"
)

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

type bucket struct {
	tokens float64
	last   time.Time
}

type ipLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*bucket
	ratePerSec float64
	burst      float64
	lastSweep  time.Time
}

func newIPLimiter(ratePerMin, burst int) *ipLimiter {
	return &ipLimiter{buckets: map[string]*bucket{}, ratePerSec: float64(ratePerMin) / 60, burst: float64(burst), lastSweep: time.Now()}
}

func (l *ipLimiter) Allow(ip string) (time.Duration, bool) {
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

func (l *ipLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for ip, b := range l.buckets {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.buckets, ip)
		}
	}
}

type dbSemaphore struct{ slots chan struct{} }

func newDBSemaphore(n int) *dbSemaphore { return &dbSemaphore{slots: make(chan struct{}, n)} }

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

type cacheEntry struct {
	value   any
	expires time.Time
}

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
