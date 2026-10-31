package site

import (
	"fmt"
	"strconv"
	"sync"
	"time"
)

func shortNumber(wd string) (int, bool) {
	if len(wd) == 0 || len(wd) > 2 {
		return 0, false
	}
	n, err := strconv.Atoi(wd)
	return n, err == nil && n >= 0
}

const articleCandidates = 3000

func articleSource(q, artWhere string) string {
	if !onlyDigits(q) {
		return "legislation_article a"
	}
	return fmt.Sprintf("(SELECT * FROM legislation_article a WHERE %s LIMIT %d) a", artWhere, articleCandidates)
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
