package mcpserver

import (
	"context"
	"log"
	"sync"
	"time"
)

// dataTTL is how long a value read from the corpus is kept before it is read
// again. The served database is read-only and baked into the image, so nothing
// changes under these values while a server runs; the TTL only keeps a server
// on a writable local database from holding them for ever.
var dataTTL = 6 * time.Hour

// fresh holds one value read from the corpus that searches need: the codes,
// the in-memory English titles and French texts, the articles' statistics,
// the calculated amounts. The first read waits for it (Warm does it at start).
// After dataTTL it is read again in the background while requests keep the
// value they have: a search never waits for a reload.
//
// Before 6 Nov 2026 a search reloaded an expired value itself, under the
// value's lock: the calculated amounts every 10 minutes (1,044 articles, 0.5 s
// on the XPS and seconds on the container) and the English titles every 6
// hours, and nothing warmed them after a start. A search more than 10 minutes
// after the previous one had a p50 of 752 ms and a p95 of 5.0 s (timeouts),
// against 172 ms and 2.6 s within a minute.
type fresh[T any] struct {
	mu      sync.Mutex
	v       T
	ok      bool
	at      time.Time
	loading bool
}

func (f *fresh[T]) get(ctx context.Context, load func(context.Context) (T, error)) (T, error) {
	f.mu.Lock()
	if f.ok {
		v := f.v
		if time.Since(f.at) >= dataTTL && !f.loading {
			f.loading = true
			go f.reload(load)
		}
		f.mu.Unlock()
		return v, nil
	}
	// The first read holds the lock, so callers that arrive meanwhile wait
	// for that one read instead of each starting their own.
	defer f.mu.Unlock()
	v, err := load(ctx)
	if err != nil {
		return v, err
	}
	f.v, f.ok, f.at = v, true, time.Now()
	return v, nil
}

// reload reads the value again outside any request. On failure the old value
// stays and the next try waits another dataTTL.
func (f *fresh[T]) reload(load func(context.Context) (T, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	v, err := load(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loading = false
	f.at = time.Now()
	if err != nil {
		log.Printf("mcp: reloading a cached corpus value: %v", err)
		return
	}
	f.v = v
}

// peek is the value held now, without reading it.
func (f *fresh[T]) peek() (T, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.v, f.ok
}
