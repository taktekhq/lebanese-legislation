// SPDX-License-Identifier: MIT

// linkdepth measures how many clicks it takes to reach a sample of the pages a
// site's sitemaps list, by following only plain links from the home page.
//
//	linkdepth [-base https://example.com] [-sample 300] [-rps 6] [-urls paths.txt] [-out report.md]
//
// It reads the sitemap index, draws an even sample of addresses from a few of
// its files, crawls the site's listing pages (and the parents of the sampled
// pages) at a polite rate with its own User-Agent, and prints, for each kind
// of page: how many are reached within 3 and within 5 clicks, how many are
// never reached, and how many have no in-link from a crawled page. See
// internal/linkdepth for what the crawl does and does not expand.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/linkdepth"
)

func main() {
	base := flag.String("base", "https://lebaneselegislation.com", "the site to measure")
	n := flag.Int("sample", 300, "how many sitemap addresses to sample")
	shards := flag.Int("shards", 12, "read at most this many sitemap files")
	seed := flag.Uint64("seed", 1, "random seed of the sample")
	rps := flag.Float64("rps", 6, "requests per second")
	conc := flag.Int("concurrency", 3, "parallel requests")
	maxPages := flag.Int("max-pages", 20000, "stop after fetching this many pages")
	hub := flag.String("hub", "", "regexp of the paths that list pages (default: lebaneselegislation.com's lists)")
	ua := flag.String("ua", "linkdepth/1.0 (+https://github.com/taktekhq/lebanese-legislation)", "User-Agent")
	urls := flag.String("urls", "", "measure these addresses (one path per line) instead of a sitemap sample, to compare two builds on the same pages")
	out := flag.String("out", "", "also write the report here")
	jsonOut := flag.String("json", "", "write the per-address result (depth, in-links) here")
	flag.Parse()

	var hubRe *regexp.Regexp
	if *hub != "" {
		hubRe = regexp.MustCompile(*hub)
	}
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var sample []string
	var err error
	if *urls != "" {
		sample, err = linkdepth.ReadList(*urls)
	} else {
		sample, err = linkdepth.Sample(client, *ua, *base, *n, *shards, *seed)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "linkdepth:", err)
		os.Exit(2)
	}
	parents := map[string]bool{}
	for _, p := range sample {
		if t := linkdepth.TextPrefix(p); t != "" {
			parents[t] = true
		}
	}
	expand := func(p string) bool { return parents[linkdepth.TextPrefix(p)] && !linkdepth.IsArticle(p) }
	start := time.Now()
	res, err := linkdepth.Crawl(context.Background(), linkdepth.Config{Base: *base, UA: *ua, RPS: *rps, Concurrency: *conc,
		MaxPages: *maxPages, Timeout: 45 * time.Second, Client: client, Hub: hubRe}, expand)
	if err != nil {
		fmt.Fprintln(os.Stderr, "linkdepth:", err)
		os.Exit(2)
	}
	rows := linkdepth.Summarize(sample, res)
	w := os.Stdout
	_, _ = fmt.Fprintf(w, "Measured %s: %d sampled addresses, %d pages fetched in %s, %d errors, statuses %v.\n\n",
		*base, len(sample), res.Fetched, time.Since(start).Round(time.Second), res.Errors, res.Status)
	linkdepth.WriteTable(w, rows)
	if *out != "" {
		f, err := os.Create(*out)
		if err == nil {
			_, _ = fmt.Fprintf(f, "Measured %s: %d sampled addresses, %d pages fetched, %d errors.\n\n", *base, len(sample), res.Fetched, res.Errors)
			linkdepth.WriteTable(f, rows)
			_ = f.Close()
		}
	}
	if *jsonOut != "" {
		type item struct {
			Path    string `json:"path"`
			Depth   int    `json:"depth"`
			Reached bool   `json:"reached"`
			Inlinks int    `json:"inlinks"`
		}
		var items []item
		for _, p := range sample {
			d, ok := res.Depth[p]
			items = append(items, item{p, d, ok, len(res.Inlinks[p])})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
		b, _ := json.MarshalIndent(items, "", " ")
		_ = os.WriteFile(*jsonOut, b, 0o644)
	}
}
