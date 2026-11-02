// Package staticexport writes the live site as plain files.
//
// Every page is produced by the same handler that serves the site, called in
// process, so an exported page is the page a visitor gets today: same
// templates, same queries, same canonical, hreflang, robots meta and status.
// Nothing is rendered by a second implementation. (internal/static is the
// older GitHub Pages preview: a different URL shape, every page noindex. This
// package keeps every live address.)
//
// The exporter learns which paths exist from the site's own sitemaps, then
// follows the internal links it finds in the pages it renders, so the pages
// the sitemaps leave out (noindex pages, pager pages) are written too. It
// holds one 64-bit hash per path it has seen and no page bodies, so memory
// does not grow with the size of the site.
//
// A manifest records every path with its status, size and content hash; a
// rebuild reads the previous manifest and rewrites only the pages whose hash
// changed.
package staticexport

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options say what to export and where.
type Options struct {
	Out     string // directory the site is written into
	Workers int    // pages rendered in parallel (default 4)

	// Paths, when set, are the only paths rendered: no sitemap, no crawl. The
	// manifest then covers just these paths.
	Paths []string
	// Sample keeps a random Sample of the sitemap paths (Seed makes it
	// repeatable); the crawl is off, because it would grow the set again.
	Sample int
	Seed   uint64
	// Crawl follows internal links to pages that no sitemap lists.
	Crawl bool
	// Limit stops after this many pages (0: all).
	Limit int
	// Extra paths rendered by their owner (/robots.txt, /llms.txt, home pages).
	Extra []string
	// Assets, when set, is copied to the root: stylesheets, scripts, images.
	Assets fs.FS
	// Transform, when set, may rewrite a 200 response before it is hashed and
	// written (the search page gets its script here). It must not touch
	// anything else, or the fidelity check will say so.
	Transform func(path, contentType string, body []byte) []byte
	// Files are extra files to write, by path under Out (the search script).
	Files map[string][]byte
	// Shard and Shards split a full export across jobs: this run renders the
	// paths whose hash falls in its slice (Shard of Shards), including crawl
	// paths and Extra. Shards <= 1 means the whole site. Deletions are left to the job that merges the shards.
	Shard, Shards int
	// Exchange is required for a sharded crawl. Each round publishes foreign
	// links after local work drains, waits for every shard, and returns this
	// shard's incoming paths and whether any shard published links. All shards
	// must participate, even when idle. An empty global round ends the crawl.
	Exchange func(ctx context.Context, round int, paths []string) (incoming []string, active bool, err error)
	// Sink, when set, receives each new or changed page instead of a file
	// being written for it (the upload to R2). Pages whose hash equals the
	// previous manifest's are not sent.
	Sink func(e Entry, body []byte) error
	// PrevEntries is the previous manifest, when it does not live in Out (it
	// is read from the bucket before a run on a fresh runner).
	PrevEntries map[string]Entry
	// Prev is the previous manifest. When empty, <Out>/.export/manifest.jsonl
	// is used if it exists.
	Prev string
	// Host is the Host header of the in-process requests.
	Host string
	// Log receives progress lines; nil is silent.
	Log func(string, ...any)
}

// Entry is one line of the manifest.
type Entry struct {
	Path   string `json:"p"`
	Status int    `json:"s"`
	Hash   string `json:"h,omitempty"` // sha256 of the body, hex
	Size   int64  `json:"n,omitempty"` // body bytes
	Type   string `json:"t,omitempty"` // Content-Type
	File   string `json:"f,omitempty"` // file under Out
	Cache  string `json:"c,omitempty"` // the live Cache-Control, for the edge
	To     string `json:"to,omitempty"`
}

// Stats is what a run did.
type Stats struct {
	Pages     int64 // 200 responses seen
	Written   int64 // files written (new or changed)
	Unchanged int64 // identical to the previous manifest
	Redirects int64
	NotFound  int64
	Errors    int64
	Raw       int64 // body bytes of all 200 responses
	Gzip      int64 // the same bytes, gzipped (level 6), to size the upload
	Removed   int64 // paths in the previous manifest that no longer exist
	PeakHeap  uint64
	PeakSys   uint64
	Elapsed   time.Duration
	ByKind    map[string]*KindStats
	FirstErrs []string
	// RemovedKeys are the files of the removed pages, for a sink run to delete.
	RemovedKeys []string `json:"-"`
}

// KindStats is the size of one family of pages.
type KindStats struct {
	Pages int64
	Raw   int64
	Gzip  int64
}

// Run exports the site served by h.
func Run(ctx context.Context, h http.Handler, o Options) (*Stats, error) {
	if o.Shards > 1 && o.Crawl && len(o.Paths) == 0 && o.Sample == 0 {
		if o.Exchange == nil {
			return nil, fmt.Errorf("staticexport: sharded crawl requires a path exchange")
		}
		if o.Limit > 0 {
			return nil, fmt.Errorf("staticexport: sharded crawl cannot use a page limit: every shard must finish discovery")
		}
	}
	if o.Out == "" {
		return nil, fmt.Errorf("staticexport: no output directory")
	}
	if o.Workers < 1 {
		o.Workers = 4
	}
	if o.Host == "" {
		o.Host = "lebaneselegislation.com"
	}
	logf := o.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	start := time.Now()
	ex := &exporter{h: h, o: o, logf: logf, seen: newSeen(), st: &Stats{ByKind: map[string]*KindStats{}}, start: start,
		crawling: o.Crawl && len(o.Paths) == 0 && o.Sample == 0}
	if err := os.MkdirAll(filepath.Join(o.Out, ".export"), 0o755); err != nil {
		return nil, err
	}
	prev := o.Prev
	if prev == "" {
		prev = filepath.Join(o.Out, ".export", "manifest.jsonl")
	}
	var err error
	if o.PrevEntries != nil {
		ex.prev = o.PrevEntries
	} else if ex.prev, err = ReadManifest(prev); err != nil {
		return nil, err
	}
	logf("previous manifest: %d paths", len(ex.prev))

	stopMem := ex.watchMemory()
	err = ex.run(ctx)
	stopMem()
	ex.st.Elapsed = time.Since(start)
	if err != nil {
		return ex.st, err
	}
	if o.Assets != nil {
		if err := copyAssets(o.Assets, o.Out); err != nil {
			return ex.st, err
		}
	}
	if len(o.Paths) == 0 && o.Sample == 0 && o.Limit == 0 && (o.Shards <= 1 || o.Shard == 0) { // the page the edge serves for a path nothing answers
		if rec := ex.get("/__static-export-not-found__/"); rec.Code == http.StatusNotFound {
			if err := writeFile(filepath.Join(o.Out, "404.html"), rec.Body.Bytes()); err != nil {
				return ex.st, err
			}
		}
	}
	for p, b := range o.Files {
		if err := writeFile(filepath.Join(o.Out, filepath.FromSlash(p)), b); err != nil {
			return ex.st, err
		}
	}
	partial := len(o.Paths) > 0 || o.Sample > 0 || o.Limit > 0 || o.Shards > 1
	if err := ex.finish(partial); err != nil {
		return ex.st, err
	}
	return ex.st, nil
}

type exporter struct {
	h    http.Handler
	o    Options
	logf func(string, ...any)
	prev map[string]Entry
	seen *seen
	st   *Stats

	start     time.Time
	crawling  bool     // follow links: Crawl, and not a fixed list or a sample
	foreign   []string // discovered paths waiting to be sent to their owners
	inSitemap *seen    // seeds already scheduled by their owners, no exchange needed

	removedMu sync.Mutex
	removed   []string // pages that were files and no longer are

	mu      sync.Mutex
	entries chan Entry // collected by the manifest writer
	queue   []string   // links found by the crawl, claimed and waiting to be rendered
	pending int64      // paths claimed and not yet finished (rendering or queued)
	cond    *sync.Cond
}

func (ex *exporter) run(ctx context.Context) error {
	manifestTmp := filepath.Join(ex.o.Out, ".export", "manifest.jsonl.new")
	mf, err := os.Create(manifestTmp)
	if err != nil {
		return err
	}
	defer mf.Close()
	ex.entries = make(chan Entry, 256)
	var wgm sync.WaitGroup
	wgm.Add(1)
	go func() { // one writer: manifest lines in the order pages finish
		defer wgm.Done()
		enc := json.NewEncoder(mf)
		for e := range ex.entries {
			_ = enc.Encode(e)
		}
	}()

	ex.cond = sync.NewCond(&ex.mu)
	jobs := make(chan string)
	var wg sync.WaitGroup
	var firstErr atomic.Value
	for w := 0; w < ex.o.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				if ctx.Err() != nil {
					ex.done()
					continue
				}
				if err := ex.page(p); err != nil {
					firstErr.CompareAndSwap(nil, err)
				}
				ex.done()
			}
		}()
	}

	// dispatch hands a claimed path to the workers; false once the limit is
	// reached or the run is cancelled.
	dispatch := func(p string) bool {
		if ex.o.Limit > 0 && atomic.LoadInt64(&ex.st.Pages)+atomic.LoadInt64(&ex.st.Redirects)+atomic.LoadInt64(&ex.st.NotFound) >= int64(ex.o.Limit) {
			return false
		}
		select {
		case jobs <- p:
			return true
		case <-ctx.Done():
			return false
		}
	}
	shutdown := func() {
		close(jobs)
		wg.Wait()
		close(ex.entries)
		wgm.Wait()
	}

	var seeds []string
	if len(ex.o.Paths) > 0 {
		seeds = ex.o.Paths
	} else {
		seeds = append(seeds, ex.o.Extra...)
		sm, err := ex.sitemapPaths(ctx)
		if err != nil {
			shutdown()
			return err
		}
		seeds = append(seeds, sm...)
		if ex.o.Shards > 1 {
			ex.inSitemap = newSeen()
			for _, p := range seeds {
				ex.inSitemap.add(p)
			}
		}
	}
	stopped := false
	for _, p := range seeds {
		if ex.o.Shards > 1 && Owner(p, ex.o.Shards) != ex.o.Shard {
			continue
		}
		if !ex.claim(p) {
			continue
		}
		if !dispatch(p) {
			stopped = true
			break
		}
	}
	// Owners exchange discoveries rather than rendering foreign pages to read
	// their links. Rounds let a path cross any number of shard boundaries,
	// including cycles and redirects, while each page is rendered only once.
	for round := 0; ex.crawling && !stopped; round++ {
		for {
			next, more := ex.nextQueued()
			if !more {
				break
			}
			if !dispatch(next) {
				stopped = true
				break
			}
		}
		if stopped || ex.o.Shards <= 1 {
			break
		}
		ex.mu.Lock()
		foreign := ex.foreign
		ex.foreign = nil
		ex.mu.Unlock()
		incoming, active, err := ex.o.Exchange(ctx, round, foreign)
		if err != nil {
			shutdown()
			return err
		}
		if !active {
			break
		}
		ex.queueNew(incoming)
	}

	shutdown()
	if err := mf.Close(); err != nil {
		return err
	}
	if v := firstErr.Load(); v != nil {
		return v.(error)
	}
	return ctx.Err()
}

// claim says whether p is new, and marks it seen.
func (ex *exporter) claim(p string) bool {
	if !ex.seen.add(p) {
		return false
	}
	ex.mu.Lock()
	ex.pending++
	ex.mu.Unlock()
	return true
}

func (ex *exporter) done() {
	ex.mu.Lock()
	ex.pending--
	ex.cond.Broadcast()
	ex.mu.Unlock()
}

// queueNew claims the links nothing has seen yet and queues them.
func (ex *exporter) queueNew(links []string) {
	var fresh []string
	for _, l := range links {
		if ex.inSitemap != nil && ex.inSitemap.has(l) {
			continue
		}
		if !ex.seen.add(l) {
			continue
		}
		if ex.o.Shards > 1 && Owner(l, ex.o.Shards) != ex.o.Shard {
			ex.mu.Lock()
			ex.foreign = append(ex.foreign, l)
			ex.mu.Unlock()
			continue
		}
		fresh = append(fresh, l)
	}
	if len(fresh) == 0 {
		return
	}
	ex.mu.Lock()
	ex.pending += int64(len(fresh))
	ex.queue = append(ex.queue, fresh...)
	ex.cond.Broadcast()
	ex.mu.Unlock()
}

// nextQueued returns the next crawl link, waiting while workers may still
// find more; more is false when the queue is empty and nothing is in flight.
func (ex *exporter) nextQueued() (string, bool) {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	for len(ex.queue) == 0 {
		if ex.pending == 0 {
			return "", false
		}
		ex.cond.Wait()
	}
	p := ex.queue[len(ex.queue)-1]
	ex.queue = ex.queue[:len(ex.queue)-1]
	return p, true
}

// get calls the handler as a GET with no query handling beyond the path.
func (ex *exporter) get(p string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, p, nil)
	req.Host = ex.o.Host
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	ex.h.ServeHTTP(rec, req)
	return rec
}

// page renders one path and records it.
func (ex *exporter) page(p string) error {
	rec := ex.get(p)
	if rec.Code >= 500 { // one retry: a pool timeout under load is not a page's fault
		time.Sleep(200 * time.Millisecond)
		rec = ex.get(p)
	}
	e := Entry{Path: p, Status: rec.Code, Type: rec.Header().Get("Content-Type"), Cache: rec.Header().Get("Cache-Control")}
	if rec.Code != http.StatusOK {
		ex.dropFile(p) // it was a page last time; the old file must not keep answering
	}
	switch {
	case rec.Code == http.StatusOK:
		return ex.ok(p, rec, e)
	case rec.Code >= 300 && rec.Code < 400:
		e.To = rec.Header().Get("Location")
		atomic.AddInt64(&ex.st.Redirects, 1)
		if ex.crawling && strings.HasPrefix(e.To, "/") {
			if t := cleanLink(e.To); t != "" {
				ex.queueNew([]string{t})
			}
		}
	case rec.Code == http.StatusNotFound:
		atomic.AddInt64(&ex.st.NotFound, 1)
	default:
		n := atomic.AddInt64(&ex.st.Errors, 1)
		if n <= 20 {
			ex.mu.Lock()
			ex.st.FirstErrs = append(ex.st.FirstErrs, fmt.Sprintf("%s: status %d", p, rec.Code))
			ex.mu.Unlock()
		}
	}
	ex.entries <- e
	return nil
}

// dropFile deletes the file a path had in the previous build, if it had one.
func (ex *exporter) dropFile(p string) {
	old, ok := ex.prev[p]
	if !ok || old.Status != http.StatusOK || old.File == "" {
		return
	}
	if ex.o.Shards > 1 { // the job that merges the shards decides what is gone
		return
	}
	ex.removedMu.Lock()
	ex.removed = append(ex.removed, p)
	ex.st.RemovedKeys = append(ex.st.RemovedKeys, old.File)
	ex.removedMu.Unlock()
	if ex.o.Sink == nil {
		_ = os.Remove(filepath.Join(ex.o.Out, filepath.FromSlash(old.File)))
	}
}

func (ex *exporter) ok(p string, rec *httptest.ResponseRecorder, e Entry) error {
	body := rec.Body.Bytes()
	if ex.o.Transform != nil {
		body = ex.o.Transform(p, e.Type, body)
	}
	sum := sha256.Sum256(body)
	e.Hash = hex.EncodeToString(sum[:])
	e.Size = int64(len(body))
	file, err := FileFor(p, e.Type)
	if err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	e.File = file
	dest := filepath.Join(ex.o.Out, filepath.FromSlash(file))
	if old, ok := ex.prev[p]; ok && old.Hash == e.Hash && old.File == file && (ex.o.Sink != nil || fileExists(dest)) {
		atomic.AddInt64(&ex.st.Unchanged, 1)
	} else {
		if ex.o.Sink != nil {
			if err := ex.o.Sink(e, body); err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
		} else if err := writeFile(dest, body); err != nil {
			return err
		}
		atomic.AddInt64(&ex.st.Written, 1)
	}
	gz := gzipSize(body)
	n := atomic.AddInt64(&ex.st.Pages, 1)
	atomic.AddInt64(&ex.st.Raw, e.Size)
	atomic.AddInt64(&ex.st.Gzip, gz)
	ex.mu.Lock()
	k := kindOf(p)
	ks := ex.st.ByKind[k]
	if ks == nil {
		ks = &KindStats{}
		ex.st.ByKind[k] = ks
	}
	ks.Pages++
	ks.Raw += e.Size
	ks.Gzip += gz
	ex.mu.Unlock()
	if n%20000 == 0 {
		ex.logf("%d pages, %.0f pages/s", n, float64(n)/time.Since(ex.start).Seconds())
	}
	if ex.crawling && strings.HasPrefix(e.Type, "text/html") {
		ex.queueNew(Links(body))
	}
	ex.entries <- e
	return nil
}

// kindOf names the family of a path for the size report: its first segment
// without the /en prefix, with the language kept apart.
func kindOf(p string) string {
	lang := "ar"
	if strings.HasPrefix(p, "/en/") || p == "/en" {
		lang = "en"
		p = strings.TrimPrefix(p, "/en")
	}
	seg := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 4)
	switch {
	case seg[0] == "":
		return lang + ":home"
	case len(seg) >= 3 && seg[2] == "article":
		return lang + ":" + seg[0] + "-article"
	case len(seg) >= 3 && seg[2] == "page", len(seg) >= 2 && seg[0] == "laws" && seg[1] == "year":
		return lang + ":" + seg[0] + "-list"
	}
	return lang + ":" + seg[0]
}

// SitemapPaths lists every path the site's sitemaps name, asking the handler.
func SitemapPaths(ctx context.Context, h http.Handler, host string) ([]string, error) {
	ex := &exporter{h: h, o: Options{Host: host}, logf: func(string, ...any) {}}
	return ex.sitemapPaths(ctx)
}

// sitemapPaths reads /sitemap.xml and every sitemap it names from the
// handler and returns their paths (a random Sample when asked).
func (ex *exporter) sitemapPaths(ctx context.Context) ([]string, error) {
	idx := ex.get("/sitemap.xml")
	if idx.Code != http.StatusOK {
		return nil, fmt.Errorf("/sitemap.xml: status %d", idx.Code)
	}
	var maps []string
	for _, l := range Locs(idx.Body.Bytes()) {
		if p, ok := pathOf(l); ok {
			maps = append(maps, p)
		}
	}
	sort.Strings(maps)
	// the sitemaps are pages of their own too
	out := append([]string{"/sitemap.xml"}, maps...)
	var rng *rand.Rand
	var reservoir []string
	var seenN int
	if ex.o.Sample > 0 {
		rng = rand.New(rand.NewPCG(ex.o.Seed, 1))
	}
	for _, m := range maps {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		rec := ex.get(m)
		if rec.Code != http.StatusOK {
			return nil, fmt.Errorf("%s: status %d", m, rec.Code)
		}
		for _, l := range Locs(rec.Body.Bytes()) {
			p, ok := pathOf(l)
			if !ok {
				continue
			}
			if rng == nil {
				out = append(out, p)
				continue
			}
			seenN++ // reservoir sampling: one pass, Sample paths in memory
			if len(reservoir) < ex.o.Sample {
				reservoir = append(reservoir, p)
			} else if j := rng.IntN(seenN); j < ex.o.Sample {
				reservoir[j] = p
			}
		}
	}
	ex.logf("sitemaps: %d shards", len(maps))
	if rng != nil {
		sort.Strings(reservoir)
		return append(out, reservoir...), nil
	}
	return out, nil
}

var locRE = regexp.MustCompile(`<loc>([^<]+)</loc>`)

// Locs are the <loc> values of a sitemap, unescaped.
func Locs(b []byte) []string {
	var out []string
	for _, m := range locRE.FindAllSubmatch(b, -1) {
		out = append(out, xmlUnescape(string(m[1])))
	}
	return out
}

var xmlRepl = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'")

func xmlUnescape(s string) string { return xmlRepl.Replace(s) }

// pathOf is the escaped path of a same-site absolute URL.
func pathOf(loc string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(loc))
	if err != nil || u.RawQuery != "" {
		return "", false
	}
	if u.Host != "" && !strings.HasSuffix(u.Host, "lebaneselegislation.com") {
		return "", false
	}
	p := u.EscapedPath()
	if !strings.HasPrefix(p, "/") {
		return "", false
	}
	return p, true
}

var linkRE = regexp.MustCompile(`(?:href|data-href)="(/[^"]*)"`)

// skipPrefixes are addresses the origin answers by itself (the API, the
// analytics proxy, files from disk) or that are not pages.
var skipPrefixes = []string{"/api/", "/ph/", "/ph", "/ul/", "/scan/", "/ruling-files/", "/official-files/", "/mcp", "/healthz", "/.well-known/", "/assets/"}

// cleanLink turns an href into a crawlable escaped path, or "".
func cleanLink(href string) string {
	href = strings.ReplaceAll(href, "&amp;", "&")
	if i := strings.IndexAny(href, "#?"); i >= 0 {
		if strings.Contains(href[i:], "?") && href[i] == '?' {
			return "" // a query: filters and search, noindex, not exported
		}
		href = href[:i]
	}
	if href == "" || strings.HasPrefix(href, "//") {
		return ""
	}
	for _, s := range skipPrefixes {
		if strings.HasPrefix(href, s) || strings.HasPrefix(strings.TrimPrefix(href, "/en"), s) {
			return ""
		}
	}
	if ext := path.Ext(href); ext != "" && ext != ".xml" && ext != ".txt" {
		return "" // an asset
	}
	return href
}

// Links are the internal page addresses linked from an HTML page.
func Links(body []byte) []string {
	var out []string
	for _, m := range linkRE.FindAllSubmatch(body, -1) {
		if l := cleanLink(string(m[1])); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// FileFor is the file, relative to the output directory, that holds the
// response for the escaped path p. Pages are <path>/index.html; other
// responses (XML, text) keep their name. The escaped form is kept on disk,
// so the edge maps a request path to a key without decoding anything.
func FileFor(p, contentType string) (string, error) {
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "\x00") {
		return "", fmt.Errorf("bad path %q", p)
	}
	segs := strings.Split(strings.TrimSuffix(p[1:], "/"), "/")
	if p != "/" {
		for _, seg := range segs {
			if seg == "" || seg == "." || seg == ".." { // "//", "/./", "/../": not an address of a page
				return "", fmt.Errorf("path %q has an empty or dot segment", p)
			}
		}
	}
	clean := path.Clean(p)
	rel := strings.TrimPrefix(clean, "/")
	if strings.HasSuffix(p, "/") || rel == "" || strings.HasPrefix(contentType, "text/html") {
		if rel == "" {
			return "index.html", nil
		}
		rel += "/index.html"
	}
	for _, seg := range strings.Split(rel, "/") {
		if len(seg) > 200 { // a file name is at most 255 bytes
			return "", fmt.Errorf("path %q has a segment longer than 200 bytes", p)
		}
	}
	return rel, nil
}

func writeFile(dest string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func gzipSize(b []byte) int64 {
	var n countWriter
	zw, _ := gzip.NewWriterLevel(&n, 6)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return int64(n)
}

type countWriter int64

func (c *countWriter) Write(b []byte) (int, error) { *c += countWriter(len(b)); return len(b), nil }

func copyAssets(src fs.FS, out string) error {
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return writeFile(filepath.Join(out, filepath.FromSlash(p)), b)
	})
}

// finish moves the new manifest into place and lists what disappeared.
func (ex *exporter) finish(partial bool) error {
	dir := filepath.Join(ex.o.Out, ".export")
	cur, err := ReadManifest(filepath.Join(dir, "manifest.jsonl.new"))
	if err != nil {
		return err
	}
	if partial { // a partial run must not forget the paths it did not visit
		for p, e := range ex.prev {
			if ex.o.Shards > 1 && Owner(p, ex.o.Shards) != ex.o.Shard {
				continue
			}
			if _, ok := cur[p]; !ok {
				cur[p] = e
			}
		}
	} else if ex.o.Shards <= 1 { // a full run: what was a page and was not visited is gone too
		for p, e := range ex.prev {
			if _, ok := cur[p]; !ok && e.Status == http.StatusOK && e.File != "" {
				if ex.o.Sink == nil {
					_ = os.Remove(filepath.Join(ex.o.Out, filepath.FromSlash(e.File)))
				}
				ex.removed = append(ex.removed, p)
				ex.st.RemovedKeys = append(ex.st.RemovedKeys, e.File)
			}
		}
	}
	sort.Strings(ex.removed)
	ex.st.Removed = int64(len(ex.removed))
	if err := os.WriteFile(filepath.Join(dir, "removed.txt"), []byte(strings.Join(ex.removed, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	if err := WriteManifest(filepath.Join(dir, "manifest.jsonl"), cur); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(dir, "manifest.jsonl.new"))
	b, _ := json.MarshalIndent(ex.st, "", "  ")
	return os.WriteFile(filepath.Join(dir, "stats.json"), b, 0o644)
}

// watchMemory samples the heap while the run lasts; call the returned func
// to stop.
func (ex *exporter) watchMemory() func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > ex.st.PeakHeap {
					ex.st.PeakHeap = m.HeapAlloc
				}
				if m.Sys > ex.st.PeakSys {
					ex.st.PeakSys = m.Sys
				}
			}
		}
	}()
	return func() { close(stop); <-done }
}

// Owner is the shard, of n, that renders a path, whether listed or crawled.
func Owner(p string, n int) int {
	h := fnv.New64a()
	_, _ = io.WriteString(h, p)
	return int(h.Sum64() % uint64(n))
}

// ---------------------------------------------------------------- seen set

// seen is a set of paths held as 64-bit hashes: 8 bytes a path plus the
// map's overhead, a tenth of the strings. A collision (about one in 10^8 for
// two million paths) would skip one page, and the fidelity check catches it.
type seen struct {
	mu sync.Mutex
	m  map[uint64]struct{}
}

func newSeen() *seen { return &seen{m: map[uint64]struct{}{}} }

func (s *seen) has(p string) bool {
	h := fnv.New64a()
	_, _ = io.WriteString(h, p)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[h.Sum64()]
	return ok
}

func (s *seen) add(p string) bool {
	h := fnv.New64a()
	_, _ = io.WriteString(h, p)
	k := h.Sum64()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[k]; ok {
		return false
	}
	s.m[k] = struct{}{}
	return true
}
