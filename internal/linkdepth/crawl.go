// SPDX-License-Identifier: MIT

package linkdepth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config is one measurement.
type Config struct {
	Base        string         // https://example.com
	UA          string         // identifies the tool to the site's owner
	RPS         float64        // requests per second across all workers
	Concurrency int            // parallel fetches
	MaxPages    int            // stop expanding after this many fetches
	Timeout     time.Duration  // per request
	Client      *http.Client   // nil: a default that does not follow redirects
	Hub         *regexp.Regexp // pages that list pages; nil: DefaultHub
}

// Result is what the crawl learned.
type Result struct {
	Depth   map[string]int      // shortest click depth of every path seen
	Inlinks map[string][]string // for each path, the crawled pages that link to it
	Fetched int
	Errors  int
	Status  map[int]int // responses by status
}

// Crawl walks from the home page level by level. A page is fetched when it is a
// hub or when expand says so (the parent of a sampled page). Redirects are
// recorded and not followed: a link that redirects is a link to a page that
// has moved, and the result should show it.
func Crawl(ctx context.Context, cfg Config, expand func(path string) bool) (*Result, error) {
	base, err := url.Parse(cfg.Base)
	if err != nil {
		return nil, err
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	res := &Result{Depth: map[string]int{"/": 0}, Inlinks: map[string][]string{}, Status: map[int]int{}}
	tick := time.NewTicker(time.Duration(float64(time.Second) / cfg.RPS))
	defer tick.Stop()
	level := []string{"/"}
	for d := 0; len(level) > 0; d++ {
		var next []string
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, cfg.Concurrency)
		for _, p := range level {
			if res.Fetched >= cfg.MaxPages {
				break
			}
			if !IsHub(p, cfg.Hub) && !expand(p) {
				continue
			}
			select {
			case <-ctx.Done():
				return res, ctx.Err()
			case <-tick.C:
			}
			res.Fetched++
			sem <- struct{}{}
			wg.Add(1)
			go func(p string) {
				defer wg.Done()
				defer func() { <-sem }()
				links, status, err := fetch(ctx, cfg, base, p)
				mu.Lock()
				defer mu.Unlock()
				res.Status[status]++
				if err != nil || status >= 500 {
					res.Errors++
				}
				for _, l := range links {
					res.Inlinks[l] = append(res.Inlinks[l], p)
					if _, seen := res.Depth[l]; !seen {
						res.Depth[l] = d + 1
						next = append(next, l)
					}
				}
			}(p)
		}
		wg.Wait()
		sort.Strings(next)
		level = next
	}
	return res, nil
}

func fetch(ctx context.Context, cfg Config, base *url.URL, p string) ([]string, int, error) {
	u := *base
	u.Path, u.RawPath = "", ""
	u2, err := u.Parse(p)
	if err != nil {
		return nil, 0, err
	}
	var last error
	for try := 0; try < 3; try++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u2.String(), nil)
		req.Header.Set("User-Agent", cfg.UA)
		req.Header.Set("Accept", "text/html")
		resp, err := cfg.Client.Do(req)
		if err != nil {
			last = err
			time.Sleep(time.Duration(try+1) * 2 * time.Second)
			continue
		}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			last = fmt.Errorf("%s: %s", p, resp.Status)
			time.Sleep(time.Duration(try+1) * 5 * time.Second)
			continue
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "html") {
			_, _ = io.Copy(io.Discard, resp.Body)
			return nil, resp.StatusCode, nil
		}
		return Links(u2, resp.Body), resp.StatusCode, nil
	}
	return nil, 599, last
}
