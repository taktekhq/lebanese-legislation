package site

import (
	"strconv"
	"sync"
	"time"
)

// A search word of one or two digits ("2", "28") is a text's number, not a
// piece of a title: LIKE '%2%' matches most titles, cannot use the trigram
// index and took 1.8 s on 103k titles (21 Oct 2026). number_int is indexed.
func shortNumber(wd string) (int, bool) {
	if len(wd) == 0 || len(wd) > 2 {
		return 0, false
	}
	n, err := strconv.Atoi(wd)
	return n, err == nil && n >= 0
}

func onlyDigits(q string) bool {
	seen := false
	for _, r := range q {
		switch {
		case r >= '0' && r <= '9':
			seen = true
		case r == ' ':
		default:
			return false
		}
	}
	return seen
}

// searchCache keeps answers to searches for an hour, up to 2,000 of them; when
// full it starts again empty, which is simpler than an LRU and costs one miss
// per question. The zero value is ready to use.
type searchCache struct {
	mu sync.Mutex
	m  map[string]cachedSearch
}

type cachedSearch struct {
	res Results
	how string
	at  time.Time
}

const (
	searchCacheTTL  = time.Hour
	searchCacheSize = 2000
)

func (c *searchCache) get(q string) (Results, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[q]
	if !ok || time.Since(e.at) > searchCacheTTL {
		return Results{}, "", false
	}
	return e.res, e.how, true
}

func (c *searchCache) put(q string, res Results, how string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= searchCacheSize {
		c.m = make(map[string]cachedSearch, 256)
	}
	c.m[q] = cachedSearch{res, how, time.Now()}
}
