// Package memo keeps answers that do not change while a server runs, for a
// while: a list's total and its filter chips. With cursor pages a page takes
// under a millisecond and the count(*) beside it became the cost (about 33 ms
// on /api/v1/laws/, 80 ms for the MCP's list_legislation, sequential scans:
// an expression index gives no index-only count). The served database is
// read-only and baked into the image, so nothing changes under a memo while a
// container runs; on a writable local database an answer can lag a change by
// the memo's TTL. It imports nothing of the project.
package memo

import (
	"fmt"
	"sync"
	"time"
)

// Map is a memo of answers by key. Its zero value is ready, with a TTL of
// ten minutes and room for 2,048 keys; past that it starts again.
type Map struct {
	TTL time.Duration
	Max int

	mu sync.Mutex
	m  map[string]entry
}

type entry struct {
	v   any
	exp time.Time
}

// Key is the key of one query with its arguments.
func Key(sql string, args ...any) string { return fmt.Sprintf("%s\x00%v", sql, args) }

// Get is the answer kept for k, if it has not expired.
func (c *Map) Get(k string) (any, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Now().After(e.exp) {
		return nil, false
	}
	return e.v, true
}

// Put keeps v for k.
func (c *Map) Put(k string, v any) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl, max := c.TTL, c.Max
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	if max == 0 {
		max = 2048
	}
	if c.m == nil || len(c.m) >= max {
		c.m = map[string]entry{}
	}
	c.m[k] = entry{v, time.Now().Add(ttl)}
}
