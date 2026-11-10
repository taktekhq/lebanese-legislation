package staticexport

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Old addresses: the ones Google and other sites still hold from before the
// moves of 16, 21 and 24 October.

// OldVariants derives old-style addresses from exported pages, n of them.
// Each is the address of a page that exists, in a form the live site
// redirects: under /ar/, without its final slash, with the wrong word for its
// text, an Arabic-Indic digit for its article number; plus the scanned texts
// that have been replaced.
func OldVariants(m map[string]Entry, d *RedirectData, n int, seed uint64) []string {
	var pages []string
	for p, e := range m {
		if e.Status == 200 && strings.HasPrefix(e.Type, "text/html") && !strings.HasPrefix(p, "/en/") && !strings.HasPrefix(p, "/fr/") {
			pages = append(pages, p)
		}
	}
	sort.Strings(pages)
	rng := rand.New(rand.NewPCG(seed, 11))
	rng.Shuffle(len(pages), func(i, j int) { pages[i], pages[j] = pages[j], pages[i] })
	set := map[string]bool{}
	add := func(p string) {
		if len(set) < n && p != "" {
			set[p] = true
		}
	}
	other := func(w string, k int) string { // the k-th wrong word
		var rest []string
		for _, x := range addressWords {
			if x != w {
				rest = append(rest, x)
			}
		}
		return rest[k%2]
	}
	third := n / 6
	for i, p := range pages {
		if len(set) >= n-len(d.Aliases[:min(len(d.Aliases), third)]) {
			break
		}
		switch i % 5 {
		case 0:
			add("/ar" + p)
		case 1:
			add(strings.TrimSuffix(p, "/"))
		case 2:
			if w, id, rest, ok := textAddress(p); ok {
				add("/" + other(w, i) + "/" + strconv.Itoa(id) + "/" + rest)
			}
		case 3:
			if w, id, rest, ok := textAddress(p); ok && strings.HasPrefix(rest, "article/") {
				seg := strings.TrimSuffix(strings.TrimPrefix(rest, "article/"), "/")
				if _, err := strconv.Atoi(seg); err == nil {
					add("/" + w + "/" + strconv.Itoa(id) + "/article/" + url.PathEscape(indic(seg)) + "/")
				}
			}
		case 4:
			if w, id, rest, ok := textAddress(p); ok {
				add("/ar/" + other(w, i+1) + "/" + strconv.Itoa(id) + "/" + strings.TrimSuffix(rest, "/"))
			}
		}
	}
	for i := 0; i < len(d.Aliases) && i < third; i++ {
		a := d.Aliases[rng.IntN(len(d.Aliases))]
		add("/law/" + strconv.Itoa(a[0]) + "/")
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// indic writes ASCII digits as Arabic-Indic digits.
func indic(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			r = '٠' + (r - '0')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// OldResult is the live answer to an old address next to the table's.
type OldResult struct {
	Path     string
	Live     int
	LiveTo   string // the live Location path
	Table    string // where Resolve sends the path ("" when it leaves it)
	Category string
}

// Categories of an old-address check.
const (
	OldMatch        = "table gives the live redirect"
	OldServed       = "live serves it, table leaves it"
	OldNeedsOrigin  = "live redirects, table does not (origin answers)"
	OldWrongTarget  = "table redirects elsewhere"
	OldTableExtra   = "table redirects, live does not"
	OldGoneBoth     = "live 404, table leaves it (origin answers)"
	OldLiveError    = "live fetch failed"
	OldUnexpected   = "other"
	oldLiveRedirect = 301
)

// CheckOld asks the live site about each old address and compares its answer
// with what the redirect table would do.
func CheckOld(ctx context.Context, c *Client, paths []string, d *RedirectData, exists func(string) bool) []OldResult {
	out := make([]OldResult, len(paths))
	var wg sync.WaitGroup
	jobs := make(chan int)
	for w := 0; w < c.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				p := paths[i]
				live := c.Get(ctx, p)
				r := OldResult{Path: p, Live: live.Status}
				if live.Err != nil {
					r.Category = OldLiveError
					out[i] = r
					continue
				}
				if u, err := url.Parse(live.Location); err == nil {
					r.LiveTo = u.EscapedPath() + rawQ(u)
				}
				to := d.Resolve(p, exists)
				if to != p {
					r.Table = to
				}
				switch {
				case live.Status == oldLiveRedirect && r.Table != "" && r.Table == r.LiveTo:
					r.Category = OldMatch
				case live.Status == oldLiveRedirect && r.Table != "":
					r.Category = OldWrongTarget
				case live.Status == oldLiveRedirect:
					r.Category = OldNeedsOrigin
				case live.Status == http.StatusOK && r.Table == "":
					r.Category = OldServed
				case r.Table != "":
					r.Category = OldTableExtra
				case live.Status == http.StatusNotFound:
					r.Category = OldGoneBoth
				default:
					r.Category = OldUnexpected
				}
				out[i] = r
				time.Sleep(c.Pause)
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return out
}

// Summary counts results by category.
func Summary[T any](rs []T, cat func(T) string) map[string]int {
	m := map[string]int{}
	for _, r := range rs {
		m[cat(r)]++
	}
	return m
}

// FormatCounts prints a category count map, largest first.
func FormatCounts(m map[string]int) string {
	type kv struct {
		k string
		v int
	}
	var l []kv
	for k, v := range m {
		l = append(l, kv{k, v})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].v > l[j].v || l[i].v == l[j].v && l[i].k < l[j].k })
	var b strings.Builder
	for _, x := range l {
		fmt.Fprintf(&b, "  %6d  %s\n", x.v, x.k)
	}
	return b.String()
}
