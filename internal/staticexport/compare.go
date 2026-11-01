package staticexport

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Fidelity: an exported page against the live one. Both are produced by the
// same code, so the differences that remain are the interesting ones: data
// the live database has and the exporter's does not (or the other way
// round), and anything inherently per request.

// Normalize makes two renderings of one page comparable: runs of whitespace
// collapse to one space, and the build id in asset links (?v=<build>) goes.
func Normalize(b []byte) string {
	b = assetVer.ReplaceAll(b, nil)
	return strings.TrimSpace(strings.Join(strings.Fields(string(b)), " "))
}

var assetVer = regexp.MustCompile(`\?v=[A-Za-z0-9._-]+`)

var (
	titleRE   = regexp.MustCompile(`(?s)<title[^>]*>(.*?)</title>`)
	canonRE   = regexp.MustCompile(`<link rel="canonical" href="([^"]*)"`)
	robotsRE  = regexp.MustCompile(`<meta name="robots" content="([^"]*)"`)
	hreflang  = regexp.MustCompile(`<link rel="alternate" hreflang="([^"]*)" href="([^"]*)"`)
	jsonLD    = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	headEndRE = regexp.MustCompile(`(?s)</head>`)
	tagRE     = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<[^>]+>`)
)

// Categories a comparison can return.
const (
	CatIdentical   = "identical"
	CatWhitespace  = "whitespace only"
	CatAssetOnly   = "asset version only"
	CatTitle       = "title"
	CatCanonical   = "canonical"
	CatRobots      = "robots meta"
	CatHreflang    = "hreflang"
	CatJSONLD      = "structured data"
	CatText        = "visible text"
	CatMarkup      = "markup only"
	CatStatus      = "status"
	CatRedirect    = "redirect target"
	CatMissingFile = "not exported"
	CatEdge        = "Cloudflare edge rewrite only (analytics beacon, email obfuscation)"
)

// Compare says how the exported page differs from the live one; a page can
// differ in several ways. The second result is a short excerpt of the first
// difference in the normalised markup, for the report.
func Compare(live, exported []byte) ([]string, string) {
	if bytes.Equal(live, exported) {
		return []string{CatIdentical}, ""
	}
	if clean, edge, mail := stripEdge(live); edge {
		if cats, ex := Compare(clean, maskEmails(exported, mail)); len(cats) == 1 && (cats[0] == CatIdentical || cats[0] == CatWhitespace || cats[0] == CatAssetOnly) {
			return []string{CatEdge}, ""
		} else {
			return cats, ex
		}
	}
	a, b := Normalize(live), Normalize(exported)
	if a == b {
		if strings.Join(strings.Fields(string(live)), " ") == strings.Join(strings.Fields(string(exported)), " ") {
			return []string{CatWhitespace}, ""
		}
		return []string{CatAssetOnly}, ""
	}
	var cats []string
	add := func(c string, differ bool) {
		if differ {
			cats = append(cats, c)
		}
	}
	add(CatTitle, first(titleRE, live) != first(titleRE, exported))
	add(CatCanonical, first(canonRE, live) != first(canonRE, exported))
	add(CatRobots, first(robotsRE, live) != first(robotsRE, exported))
	add(CatHreflang, all(hreflang, live) != all(hreflang, exported))
	add(CatJSONLD, all(jsonLD, live) != all(jsonLD, exported))
	add(CatText, visibleText(live) != visibleText(exported))
	if len(cats) == 0 {
		cats = append(cats, CatMarkup)
	}
	return cats, excerpt(a, b)
}

var (
	beaconRE  = regexp.MustCompile(`(?s)<script[^>]*(?:static\.cloudflareinsights\.com/beacon|/cdn-cgi/scripts/[^"]*/email-decode\.min\.js)[^>]*>\s*</script>`)
	cfEmailRE = regexp.MustCompile(`(?s)<a\b[^>]*href="/cdn-cgi/l/email-protection[^"]*"[^>]*>.*?</a>|<span class="__cf_email__"[^>]*>.*?</span>`)
	mailtoRE  = regexp.MustCompile(`(?s)<a\b[^>]*href="mailto:[^"]*"[^>]*>.*?</a>`)
	emailRE   = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)+`)
)

// stripEdge removes what Cloudflare's edge adds to a live page: the Web
// Analytics beacon (for requests that accept HTML) and the obfuscation of
// e-mail addresses, with the script that undoes it. It says whether it found
// anything, and whether that included addresses.
func stripEdge(live []byte) (out []byte, edge, mail bool) {
	out = beaconRE.ReplaceAll(live, nil)
	edge = len(out) != len(live)
	if cfEmailRE.Match(out) {
		out, edge, mail = cfEmailRE.ReplaceAll(out, []byte("EMAIL")), true, true
	}
	if mail {
		out = emailRE.ReplaceAll(out, []byte("EMAIL"))
	}
	return out, edge, mail
}

// maskEmails makes the exported page comparable with a live page whose
// addresses were obfuscated: a mailto link or a bare address becomes the same
// token the live side got. (The edge leaves some addresses alone, such as a
// placeholder; both sides mask those.)
func maskEmails(b []byte, mail bool) []byte {
	if !mail {
		return b
	}
	return emailRE.ReplaceAll(mailtoRE.ReplaceAll(b, []byte("EMAIL")), []byte("EMAIL"))
}

func first(re *regexp.Regexp, b []byte) string {
	if m := re.FindSubmatch(b); m != nil {
		return strings.Join(strings.Fields(string(m[1])), " ")
	}
	return ""
}

func all(re *regexp.Regexp, b []byte) string {
	var parts []string
	for _, m := range re.FindAllSubmatch(b, -1) {
		var one []string
		for _, g := range m[1:] {
			one = append(one, string(g))
		}
		parts = append(parts, strings.Join(strings.Fields(strings.Join(one, " ")), " "))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

// visibleText is the page's text with its markup, scripts and styles gone.
func visibleText(b []byte) string {
	if loc := headEndRE.FindIndex(b); loc != nil {
		b = b[loc[1]:]
	}
	return strings.Join(strings.Fields(tagRE.ReplaceAllString(string(b), " ")), " ")
}

var digitRE = regexp.MustCompile(`[0-9\x{0660}-\x{0669}]+`)

// Signature names a difference so pages that differ for one reason group
// together: the markup tokens (split at tag ends, digits masked) that only the
// live page has, and those only the export has.
func Signature(live, exported []byte) string {
	clean, edge, mail := stripEdge(live)
	if edge {
		exported = maskEmails(exported, mail)
	}
	count := func(b []byte) map[string]int {
		m := map[string]int{}
		for _, t := range strings.SplitAfter(Normalize(b), ">") {
			if t = strings.TrimSpace(t); t != "" {
				m[digitRE.ReplaceAllString(t, "#")]++
			}
		}
		return m
	}
	a, b := count(clean), count(exported)
	var onlyLive, onlyOut []string
	for t, n := range a {
		if n > b[t] {
			onlyLive = append(onlyLive, short(t))
		}
	}
	for t, n := range b {
		if n > a[t] {
			onlyOut = append(onlyOut, short(t))
		}
	}
	sort.Strings(onlyLive)
	sort.Strings(onlyOut)
	return "live only: " + join(onlyLive, 3) + "\n    export only: " + join(onlyOut, 3)
}

func short(t string) string {
	if r := []rune(t); len(r) > 110 {
		return string(r[:110]) + "…"
	}
	return t
}

func join(l []string, n int) string {
	if len(l) == 0 {
		return "(nothing)"
	}
	extra := ""
	if len(l) > n {
		extra = fmt.Sprintf(" (+%d more)", len(l)-n)
		l = l[:n]
	}
	return strings.Join(l, " ¦ ") + extra
}

// excerpt shows both renderings around the first byte where they differ.
func excerpt(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	lo := max(i-60, 0)
	cut := func(s string) string { return s[lo:min(i+100, len(s))] }
	return fmt.Sprintf("live: …%s\n  out: …%s", cut(a), cut(b))
}

// ---------------------------------------------------------------- live side

// Client fetches live pages politely: a fixed User-Agent that says who is
// asking, a pause between a worker's requests, no redirect following.
type Client struct {
	Base    string
	Pause   time.Duration
	Workers int
	HTTP    *http.Client
	// SaveDir, when set, keeps every live 200 body there (see LiveFile), so
	// differences can be studied again without asking the live site twice.
	SaveDir string
	// FromDir, when set, reads live bodies kept earlier by SaveDir instead of
	// asking the live site.
	FromDir string
}

// LiveFile is where Client keeps the live body of path p.
func LiveFile(dir, p string) string {
	sum := sha1.Sum([]byte(p))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".html")
}

// NewClient returns a client for the live site.
func NewClient(base string, workers int, pause time.Duration) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Workers: max(workers, 1), Pause: pause,
		HTTP: &http.Client{Timeout: 60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Live is a live response.
type Live struct {
	Status   int
	Location string
	Type     string
	Body     []byte
	Err      error
}

// Get fetches one escaped path.
func (c *Client) Get(ctx context.Context, p string) Live {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+p, nil)
	if err != nil {
		return Live{Err: err}
	}
	req.Header.Set("User-Agent", "lebleg-static-export-fidelity/1 (+https://taktek.io; one-off comparison, low rate)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8") // as a browser: the edge adds its beacon only then
	var last Live
	for try := 0; try < 3; try++ {
		resp, err := c.HTTP.Do(req)
		if err != nil {
			last = Live{Err: err}
			time.Sleep(time.Duration(try+1) * time.Second)
			continue
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			last = Live{Err: err}
			continue
		}
		last = Live{Status: resp.StatusCode, Location: resp.Header.Get("Location"), Type: resp.Header.Get("Content-Type"), Body: b}
		if resp.StatusCode < 500 {
			return last
		}
		time.Sleep(time.Duration(try+1) * time.Second)
	}
	return last
}

// SamplePaths draws n paths at random from the live sitemaps.
func (c *Client) SamplePaths(ctx context.Context, n int, seed uint64) ([]string, error) {
	idx := c.Get(ctx, "/sitemap.xml")
	if idx.Err != nil || idx.Status != http.StatusOK {
		return nil, fmt.Errorf("/sitemap.xml: status %d: %v", idx.Status, idx.Err)
	}
	rng := rand.New(rand.NewPCG(seed, 7))
	var res []string
	seen := 0
	for _, l := range Locs(idx.Body) {
		p, ok := pathOf(l)
		if !ok {
			continue
		}
		time.Sleep(c.Pause)
		sm := c.Get(ctx, p)
		if sm.Err != nil || sm.Status != http.StatusOK {
			return nil, fmt.Errorf("%s: status %d: %v", p, sm.Status, sm.Err)
		}
		for _, l := range Locs(sm.Body) {
			p, ok := pathOf(l)
			if !ok {
				continue
			}
			seen++
			if len(res) < n {
				res = append(res, p)
			} else if j := rng.IntN(seen); j < n {
				res[j] = p
			}
		}
	}
	sort.Strings(res)
	return res, nil
}

// Result is the comparison of one path.
type Result struct {
	Path    string
	Cats    []string
	Excerpt string
	Detail  string
	Sig     string // see Signature: set for pages that differ beyond the edge rewrites
}

// Report compares every path against the live site, with the exported pages
// read by read(p) (nil when the path was not exported).
func (c *Client) Report(ctx context.Context, paths []string, m map[string]Entry, read func(Entry) ([]byte, error)) []Result {
	out := make([]Result, len(paths))
	var wg sync.WaitGroup
	jobs := make(chan int)
	for w := 0; w < c.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				out[i] = c.one(ctx, paths[i], m, read)
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

func (c *Client) one(ctx context.Context, p string, m map[string]Entry, read func(Entry) ([]byte, error)) Result {
	r := Result{Path: p}
	var live Live
	if c.FromDir != "" {
		if b, err := os.ReadFile(LiveFile(c.FromDir, p)); err == nil {
			live = Live{Status: http.StatusOK, Body: b, Type: "text/html"}
		} else {
			live = Live{Err: fmt.Errorf("no saved live page for %s", p)}
		}
	} else {
		live = c.Get(ctx, p)
	}
	if live.Err != nil {
		r.Cats, r.Detail = []string{CatStatus}, "live fetch failed: "+live.Err.Error()
		return r
	}
	if c.SaveDir != "" && live.Status == http.StatusOK {
		_ = os.WriteFile(LiveFile(c.SaveDir, p), live.Body, 0o644)
	}
	e, ok := m[p]
	if !ok {
		r.Cats, r.Detail = []string{CatMissingFile}, fmt.Sprintf("live status %d", live.Status)
		return r
	}
	if e.Status != live.Status {
		r.Cats, r.Detail = []string{CatStatus}, fmt.Sprintf("live %d, export %d", live.Status, e.Status)
		return r
	}
	if live.Status >= 300 && live.Status < 400 {
		if u, err := url.Parse(live.Location); err == nil && u.EscapedPath()+rawQ(u) == e.To {
			r.Cats = []string{CatIdentical}
		} else {
			r.Cats, r.Detail = []string{CatRedirect}, fmt.Sprintf("live %s, export %s", live.Location, e.To)
		}
		return r
	}
	if live.Status != http.StatusOK {
		r.Cats = []string{CatIdentical}
		return r
	}
	b, err := read(e)
	if err != nil {
		r.Cats, r.Detail = []string{CatMissingFile}, err.Error()
		return r
	}
	r.Cats, r.Excerpt = Compare(live.Body, b)
	if len(r.Cats) > 0 && r.Cats[0] != CatIdentical && r.Cats[0] != CatEdge {
		r.Sig = Signature(live.Body, b)
	}
	return r
}

func rawQ(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	return "?" + u.RawQuery
}
