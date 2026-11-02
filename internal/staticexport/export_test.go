package staticexport

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

// fakeSite is a handler shaped like the live site: sitemaps, pages that link
// to a page no sitemap lists, a moved address, a missing page.
func fakeSite(pages map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.EscapedPath()
		switch {
		case p == "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<sitemapindex><sitemap><loc>https://lebaneselegislation.com/sitemaps/a.xml</loc></sitemap></sitemapindex>`)
		case p == "/sitemaps/a.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<urlset><url><loc>https://lebaneselegislation.com/law/1/</loc></url>`+
				`<url><loc>https://lebaneselegislation.com/decree/2/article/1-%D8%A7/</loc></url>`+
				`<url><loc>https://lebaneselegislation.com/en/law/1/</loc></url></urlset>`)
		case p == "/old/":
			http.Redirect(w, r, "/law/1/", http.StatusMovedPermanently)
		case strings.HasPrefix(p, "/api/"):
			http.Error(w, "must not be crawled", 500)
		default:
			body, ok := pages[p]
			if !ok {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, "<html><body>page not found</body></html>")
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=300, s-maxage=43200")
			fmt.Fprint(w, body)
		}
	})
}

func site() map[string]string {
	return map[string]string{
		"/law/1/":                     `<html><body><a href="/decree/2/article/1-%D8%A7/">a</a> <a href="/hidden/">h</a> <a href="/old/">o</a> <a href="/laws/?sort=x">f</a> <a href="/api/v1/x/">api</a> <a href="/styles.css">css</a> <a href="/nope/">n</a></body></html>`,
		"/decree/2/article/1-%D8%A7/": `<html><body>article one</body></html>`,
		"/en/law/1/":                  `<html><body>law one in English</body></html>`,
		"/hidden/":                    `<html><body>noindex page, linked only</body></html>`,
		"/":                           `<html><body>home</body></html>`,
	}
}

func run(t *testing.T, h http.Handler, o Options) (*Stats, string) {
	t.Helper()
	if o.Out == "" {
		o.Out = t.TempDir()
	}
	st, err := Run(context.Background(), h, o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return st, o.Out
}

func TestRunWritesSitemapAndLinkedPages(t *testing.T) {
	st, out := run(t, fakeSite(site()), Options{Crawl: true, Extra: []string{"/"}, Workers: 3,
		Assets: fstest.MapFS{"styles.css": {Data: []byte("body{}")}}})
	for _, f := range []string{"law/1/index.html", "decree/2/article/1-%D8%A7/index.html", "en/law/1/index.html",
		"hidden/index.html", "index.html", "sitemap.xml", "sitemaps/a.xml", "styles.css"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(out, "404.html")); err != nil || !strings.Contains(string(b), "not found") {
		t.Errorf("404.html = %q, %v", b, err)
	}
	if st.Redirects != 1 || st.NotFound != 1 || st.Errors != 0 {
		t.Errorf("redirects=%d notfound=%d errors=%d, want 1, 1, 0 (the /api/ link must not be followed)", st.Redirects, st.NotFound, st.Errors)
	}
	m, err := ReadManifest(filepath.Join(out, ".export", "manifest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if e := m["/old/"]; e.Status != 301 || e.To != "/law/1/" {
		t.Errorf("redirect entry = %+v", e)
	}
	if e := m["/hidden/"]; e.Status != 200 || e.Hash == "" || e.File != "hidden/index.html" || e.Cache == "" {
		t.Errorf("crawled entry = %+v", e)
	}
	if _, bad := m["/api/v1/x/"]; bad {
		t.Error("the crawl followed an /api/ link")
	}
	if _, bad := m["/laws/?sort=x"]; bad {
		t.Error("the crawl followed a link with a query")
	}
}

func TestSecondRunRewritesOnlyWhatChanged(t *testing.T) {
	pages := site()
	_, out := run(t, fakeSite(pages), Options{Crawl: true, Extra: []string{"/"}})
	pages["/hidden/"] = `<html><body>noindex page, now edited</body></html>`
	delete(pages, "/en/law/1/")
	st, _ := run(t, fakeSite(pages), Options{Out: out, Crawl: true, Extra: []string{"/"}})
	if st.Written != 1 {
		t.Errorf("rewrote %d pages, want exactly the edited one", st.Written)
	}
	if st.Unchanged < 3 {
		t.Errorf("unchanged = %d, want the untouched pages", st.Unchanged)
	}
	if st.Removed != 1 {
		t.Errorf("removed = %d, want 1 (the English law page)", st.Removed)
	}
	b, _ := os.ReadFile(filepath.Join(out, ".export", "removed.txt"))
	if !strings.Contains(string(b), "/en/law/1/") {
		t.Errorf("removed.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(out, "en", "law", "1", "index.html")); err == nil {
		t.Error("the file of a page that now answers 404 is still there")
	}
	got, _ := os.ReadFile(filepath.Join(out, "hidden", "index.html"))
	if !strings.Contains(string(got), "now edited") {
		t.Errorf("the changed page was not rewritten: %q", got)
	}
}

func TestPathsAndSampleAreExactAndRepeatable(t *testing.T) {
	st, out := run(t, fakeSite(site()), Options{Paths: []string{"/hidden/", "/law/1/"}, Crawl: true})
	if st.Pages != 2 {
		t.Errorf("Paths exported %d pages, want 2 (no crawl)", st.Pages)
	}
	if _, err := os.Stat(filepath.Join(out, "decree")); err == nil {
		t.Error("Paths run followed a link")
	}
	a, outA := run(t, fakeSite(site()), Options{Sample: 2, Seed: 9})
	b, outB := run(t, fakeSite(site()), Options{Sample: 2, Seed: 9})
	ma, _ := ReadManifest(filepath.Join(outA, ".export", "manifest.jsonl"))
	mb, _ := ReadManifest(filepath.Join(outB, ".export", "manifest.jsonl"))
	if a.Pages != b.Pages || len(ma) != len(mb) {
		t.Fatalf("sample differs between runs: %d/%d pages", a.Pages, b.Pages)
	}
	for p := range ma {
		if _, ok := mb[p]; !ok {
			t.Errorf("%s only in the first sample", p)
		}
	}
}

func TestPartialRunKeepsPreviousEntries(t *testing.T) {
	_, out := run(t, fakeSite(site()), Options{Crawl: true, Extra: []string{"/"}})
	run(t, fakeSite(site()), Options{Out: out, Paths: []string{"/hidden/"}})
	m, _ := ReadManifest(filepath.Join(out, ".export", "manifest.jsonl"))
	if _, ok := m["/law/1/"]; !ok {
		t.Error("a partial run dropped a path it did not visit")
	}
}

func TestFileFor(t *testing.T) {
	for _, c := range []struct{ path, ctype, want string }{
		{"/", "text/html", "index.html"},
		{"/law/12/", "text/html; charset=utf-8", "law/12/index.html"},
		{"/decree/2/article/1-%D8%A7/", "text/html", "decree/2/article/1-%D8%A7/index.html"},
		{"/sitemap.xml", "application/xml", "sitemap.xml"},
		{"/sitemaps/laws-1.xml", "application/xml", "sitemaps/laws-1.xml"},
		{"/robots.txt", "text/plain", "robots.txt"},
		{"/en/", "text/html", "en/index.html"},
	} {
		got, err := FileFor(c.path, c.ctype)
		if err != nil || got != c.want {
			t.Errorf("FileFor(%q) = %q, %v; want %q", c.path, got, err, c.want)
		}
	}
	for _, bad := range []string{"/../etc/passwd", "/a/../../b/", "law/1/", "/x\x00y/", "/" + strings.Repeat("a", 201) + "/"} {
		if got, err := FileFor(bad, "text/html"); err == nil {
			t.Errorf("FileFor(%q) = %q, want an error", bad, got)
		}
	}
}

func TestLinks(t *testing.T) {
	html := `<a href="/law/1/">x</a><a href="/en/decree/2/article/3/#a">y</a><a href="/laws/?q=1">q</a>
	<a href="//evil.example/">e</a><a href="https://other.example/">o</a><a href="/api/v1/laws/">api</a>
	<a href="/en/api/v1/">api</a><a href="/ph/e/">ph</a><link href="/styles.css"><a href="/data/laws.parquet">f</a>
	<a href="/changes/feed.xml">feed</a><a data-href="/codes/">c</a>`
	want := map[string]bool{"/law/1/": true, "/en/decree/2/article/3/": true, "/changes/feed.xml": true, "/codes/": true}
	got := Links([]byte(html))
	if len(got) != len(want) {
		t.Fatalf("Links = %v, want %v", got, want)
	}
	for _, l := range got {
		if !want[l] {
			t.Errorf("unexpected link %q", l)
		}
	}
}

func TestPathOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://lebaneselegislation.com/law/1/":                     "/law/1/",
		"https://lebaneselegislation.com/decree/2/article/1-%D8%A7/": "/decree/2/article/1-%D8%A7/",
		"https://lebaneselegislation.com/sitemaps/laws-1.xml":        "/sitemaps/laws-1.xml",
		"https://other.example/law/1/":                               "",
		"https://lebaneselegislation.com/laws/?page=2":               "",
	} {
		if got, _ := pathOf(in); got != want {
			t.Errorf("pathOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchShellAddsOneScriptToSearchPagesOnly(t *testing.T) {
	tr := SearchShell("https://api.example/")
	body := []byte("<html><body>x</body></html>")
	out := string(tr("/search/", "text/html; charset=utf-8", body))
	if !strings.Contains(out, `src="/assets/search-shell.js" data-api="https://api.example"></script>`) || !strings.HasSuffix(out, "</body></html>") {
		t.Errorf("shell not injected: %s", out)
	}
	if string(tr("/en/search/", "text/html", body)) == string(body) {
		t.Error("English search page untouched")
	}
	for _, p := range []string{"/law/1/", "/", "/search/page/"} {
		if string(tr(p, "text/html", body)) != string(body) {
			t.Errorf("%s was changed", p)
		}
	}
	if _, ok := SearchShellFiles()["assets/search-shell.js"]; !ok {
		t.Error("script file missing")
	}
	if got := string(SearchShell(`a"b`)("/search/", "text/html", body)); strings.Contains(got, `a"b`) {
		t.Error("api base not escaped")
	}
}

func TestFullRunDropsPagesNoLongerLinked(t *testing.T) {
	pages := site()
	_, out := run(t, fakeSite(pages), Options{Crawl: true, Extra: []string{"/"}})
	pages["/law/1/"] = `<html><body>no link to the hidden page any more</body></html>`
	st, _ := run(t, fakeSite(pages), Options{Out: out, Crawl: true, Extra: []string{"/"}})
	if _, err := os.Stat(filepath.Join(out, "hidden", "index.html")); err == nil {
		t.Error("a page nothing links to or lists any more kept its file")
	}
	if st.Removed < 1 {
		t.Errorf("removed = %d", st.Removed)
	}
}

func TestShardsPartitionTheSitemapPaths(t *testing.T) {
	pages := site()
	pages["/law/2/"], pages["/law/3/"], pages["/law/4/"] = "<html>2</html>", "<html>3</html>", "<html>4</html>"
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<sitemapindex><sitemap><loc>https://lebaneselegislation.com/sitemaps/a.xml</loc></sitemap></sitemapindex>`)
		case "/sitemaps/a.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<urlset><url><loc>https://lebaneselegislation.com/law/1/</loc></url><url><loc>https://lebaneselegislation.com/law/2/</loc></url>`+
				`<url><loc>https://lebaneselegislation.com/law/3/</loc></url><url><loc>https://lebaneselegislation.com/law/4/</loc></url></urlset>`)
		default:
			fakeSite(pages).ServeHTTP(w, r)
		}
	})
	all := map[string]bool{}
	for i := 0; i < 3; i++ {
		_, out := run(t, h, Options{Extra: []string{"/"}, Shard: i, Shards: 3})
		m, _ := ReadManifest(filepath.Join(out, ".export", "manifest.jsonl"))
		for p, e := range m {
			if strings.HasPrefix(p, "/law/") && strings.Count(p, "/") == 3 && e.Status == 200 {
				if all[p] {
					t.Errorf("%s rendered by two shards", p)
				}
				all[p] = true
				if Owner(p, 3) != i {
					t.Errorf("%s rendered by shard %d, owned by %d", p, i, Owner(p, 3))
				}
			}
		}
	}
	for _, p := range []string{"/law/1/", "/law/2/", "/law/3/", "/law/4/"} {
		if !all[p] {
			t.Errorf("%s rendered by no shard", p)
		}
	}
}

// A chain deliberately crosses owners at every hop, including a redirect.
// Filtering foreign links without exchanging them loses the rest of the chain.
func TestShardsPartitionCrossOwnerCrawl(t *testing.T) {
	const shards = 3
	paths := []string{"/law/1/"}
	for i := 1; i < 8; i++ {
		for n := 0; ; n++ {
			p := fmt.Sprintf("/crawl-%d-%d/", i, n)
			if Owner(p, shards) == (Owner(paths[i-1], shards)+1)%shards {
				paths = append(paths, p)
				break
			}
		}
	}
	pages := site()
	for i, p := range paths {
		next := paths[(i+1)%len(paths)] // cycle and duplicate links must not rerender
		pages[p] = fmt.Sprintf(`<a href="%s">next</a><a href="%s">again</a>`, next, next)
	}
	var mu sync.Mutex
	calls := map[string]int{}
	base := fakeSite(pages)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.EscapedPath()]++
		mu.Unlock()
		if r.URL.EscapedPath() == paths[3] {
			http.Redirect(w, r, paths[4], http.StatusMovedPermanently)
			return
		}
		base.ServeHTTP(w, r)
	})
	_, out := run(t, h, Options{Crawl: true, Extra: []string{"/"}})
	want, err := ReadManifest(filepath.Join(out, ".export", "manifest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if _, ok := want[p]; !ok {
			t.Fatalf("fixture did not reach %s", p)
		}
	}
	calls = map[string]int{}

	// Use the actual R2 exchange against an in-memory HTTP object store.
	var storeMu sync.Mutex
	objects := map[string][]byte{}
	r2 := fakeR2(t, func(w http.ResponseWriter, r *http.Request) {
		storeMu.Lock()
		defer storeMu.Unlock()
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			objects[r.URL.Path] = b
			return
		}
		b, ok := objects[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(b)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type result struct {
		manifest map[string]Entry
		err      error
	}
	results := make(chan result, shards)
	for shard := 0; shard < shards; shard++ {
		out := t.TempDir()
		go func(shard int, out string) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				p := r.URL.EscapedPath()
				if p != "/sitemap.xml" && p != "/sitemaps/a.xml" && p != "/__static-export-not-found__/" && Owner(p, shards) != shard {
					t.Errorf("handler rendered foreign %s on shard %d", p, shard)
				}
				h.ServeHTTP(w, r)
			})
			_, err := Run(ctx, handler, Options{Out: out, Crawl: true, Extra: []string{"/"}, Shard: shard, Shards: shards,
				Exchange: r2.PathExchange("test-run", shard, shards)})
			if err != nil {
				results <- result{err: err}
				return
			}
			got, err := ReadManifest(filepath.Join(out, ".export", "manifest.jsonl"))
			for p := range got {
				if Owner(p, shards) != shard {
					t.Errorf("manifest has foreign %s on shard %d", p, shard)
				}
			}
			results <- result{got, err}
		}(shard, out)
	}
	union := map[string]Entry{}
	for shard := 0; shard < shards; shard++ {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		for p, e := range result.manifest {
			if _, exists := union[p]; exists {
				t.Errorf("duplicate render: %s", p)
			}
			union[p] = e
		}
	}

	if !reflect.DeepEqual(union, want) {
		t.Errorf("sharded union differs: got %v, want %v", union, want)
	}
	for _, p := range paths {
		if calls[p] != 1 {
			t.Errorf("%s rendered %d times, want 1", p, calls[p])
		}
	}
}
