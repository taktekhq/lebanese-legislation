// SPDX-License-Identifier: MIT

package linkdepth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLinksKeepsOnlyFollowableSamePathLinks(t *testing.T) {
	page, _ := url.Parse("https://example.com/laws/year/2003/")
	body := `<a href="/law/1/">a</a> <a href="page/2/">b</a> <a href="/law/1/#x">c</a> <a href="/laws/?page=2">q</a>
		<a href="https://other.org/x/">o</a> <a href="mailto:a@b.c">m</a> <a href="https://example.com/about/">abs</a> <link href="/css">`
	got := strings.Join(Links(page, strings.NewReader(body)), " ")
	want := "/about/ /law/1/ /laws/year/2003/page/2/"
	if got != want {
		t.Fatalf("Links = %q, want %q", got, want)
	}
}

func TestIsHub(t *testing.T) {
	for p, want := range map[string]bool{
		"/": true, "/en/": true, "/laws/": true, "/laws/years/": true, "/laws/year/2003/": true, "/laws/year/2003/page/2/": true, "/laws/year/2003/decree/": true, "/laws/year/2003/decree/page/3/": true, "/codes/penal/": true, "/codes/penal/page/2/": true,
		"/en/laws/year/2003/page/12/": true, "/rulings/page/3/": true, "/topic/rent-law/": true, "/codes/": true, "/code/penal/2/": true,
		"/law/244226/": false, "/codes/penal/article/1/": false, "/law/244226/part/2/": false, "/ruling/5/": false, "/gazette/12/": false, "/changes/review/": false,
		"/changes/feed.xml": false, "/blog/x/": false,
	} {
		if got := IsHub(p, nil); got != want {
			t.Errorf("IsHub(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestFamilyAndPrefix(t *testing.T) {
	cases := []struct{ p, fam, prefix string }{
		{"/law/244226/article/221/", "ar article", "/law/244226/"},
		{"/en/decree/7/", "en law", "/en/decree/7/"},
		{"/ruling/5389/", "ar ruling", "/ruling/5389/"},
		{"/decree/1/part/2/", "ar law", "/decree/1/"},
		{"/en/text/9/article/1-bis/", "en article", "/en/text/9/"},
	}
	for _, c := range cases {
		if f := Family(c.p); f != c.fam {
			t.Errorf("Family(%q) = %q, want %q", c.p, f, c.fam)
		}
		if p := TextPrefix(c.p); p != c.prefix {
			t.Errorf("TextPrefix(%q) = %q, want %q", c.p, p, c.prefix)
		}
	}
}

// site: / -> /laws/years/ -> /laws/year/2003/ -> law 1 (3) -> its article and law 2 (4);
// article 9 is listed by nothing.
func TestCrawlDepthAndUnreached(t *testing.T) {
	pages := map[string]string{
		"/":                 `<a href="/laws/years/">y</a>`,
		"/laws/years/":      `<a href="/laws/year/2003/">2003</a>`,
		"/laws/year/2003/":  `<a href="/law/1/">one</a>`,
		"/law/1/":           `<a href="/law/1/article/1/">a</a><a href="/law/2/">two</a>`,
		"/law/1/article/1/": `<a href="/law/1/article/2/">next</a>`,
		"/law/2/":           `<a href="/">home</a>`,
		"/law/9/article/9/": ``,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, b)
	}))
	defer srv.Close()
	sample := []string{"/law/1/article/1/", "/law/2/", "/law/9/article/9/"}
	parents := map[string]bool{}
	for _, p := range sample {
		parents[TextPrefix(p)] = true
	}
	res, err := Crawl(context.Background(), Config{Base: srv.URL, UA: "t", RPS: 500, Concurrency: 2, MaxPages: 50, Timeout: 5 * time.Second},
		func(p string) bool { return parents[TextPrefix(p)] && !IsArticle(p) })
	if err != nil {
		t.Fatal(err)
	}
	rows := Summarize(sample, res)
	all := rows[len(rows)-1]
	if all.N != 3 || all.Unreached != 1 || all.Within3 != 0 || all.Within5 != 2 {
		t.Fatalf("summary = %+v", all)
	}
	if d := res.Depth["/law/1/article/1/"]; d != 4 {
		t.Errorf("article depth = %d, want 4", d)
	}
	if n := len(res.Inlinks["/law/1/article/1/"]); n != 1 {
		t.Errorf("article in-links = %d, want 1", n)
	}
	if _, ok := res.Depth["/law/1/article/2/"]; ok {
		t.Errorf("an article page is a leaf of the crawl, its links are not followed: %v", res.Depth)
	}
}
