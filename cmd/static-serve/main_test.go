package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerKeys(t *testing.T) {
	for _, c := range [][2]string{{"/", "index.html"}, {"/law/12/", "law/12/index.html"}, {"/decree/2/article/1-%d8%a7/", "decree/2/article/1-%D8%A7/index.html"}, {"/sitemap.xml", "sitemap.xml"}, {"/en/", "en/index.html"}, {"/_export/manifest.jsonl", ""}, {"/_build/foo", ""}, {"/a/../b/", ""}} {
		if got := keyFor(c[0]); got != c[1] {
			t.Errorf("keyFor(%q)=%q, want %q", c[0], got, c[1])
		}
	}
}
func TestWorkerRedirectFixtures(t *testing.T) {
	b, err := os.ReadFile("../../internal/staticexport/testdata/resolve-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var d RedirectData
	if err = json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	d.index()
	var fixture struct {
		Pages []string
		Cases [][2]string
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	exists := func(p string) bool {
		for _, page := range fixture.Pages {
			if page == p {
				return true
			}
		}
		return false
	}
	for _, c := range fixture.Cases {
		if got := d.Resolve(c[0], exists); got != c[1] {
			t.Errorf("Resolve(%q)=%q, want %q", c[0], got, c[1])
		}
	}
}
func TestWorkerRouting(t *testing.T) {
	for _, c := range []struct {
		method, path string
		dynamic      bool
	}{
		{"GET", "/law/1/", false}, {"HEAD", "/en/decree/2/article/3/", false}, {"POST", "/search/", true},
		{"GET", "/law/1/?utm_source=chatgpt.com&gclid=x", false}, {"GET", "/laws/?kind=decree", true},
		{"GET", "/search/?q=labour", true}, {"GET", "/law/1/go/?n=5", true}, {"GET", "/phrases/", false},
	} {
		if got := dynamic(httptest.NewRequest(c.method, c.path, nil)); got != c.dynamic {
			t.Errorf("%s %s: dynamic=%v", c.method, c.path, got)
		}
	}
	for _, p := range []string{"/api/v1/search/", "/ph/e/", "/ph", "/mcp", "/healthz", "/.well-known/x", "/ul/uploads/images/a.png", "/scan/1/a.png", "/ruling-files/9.pdf", "/official/", "/en/official/3/", "/official-files/1/a"} {
		if !dynamic(httptest.NewRequest("GET", p, nil)) {
			t.Errorf("%s must be dynamic", p)
		}
	}
}
func TestServeFilesRedirectsAnd404(t *testing.T) {
	dir := t.TempDir()
	for key, body := range map[string]string{"index.html": "home", "404.html": "missing", "law/7/index.html": "law", "decree/2/article/%D8%A7/index.html": "escaped"} {
		name := filepath.Join(dir, key)
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	s := &site{root: root}
	s.redirects.index()
	for _, c := range []struct {
		path           string
		status         int
		body, location string
	}{
		{"/", 200, "home", ""}, {"/law/7/?utm_source=x", 200, "law", ""},
		{"/ar/law/7/?utm_source=x", 301, "", "/law/7/?utm_source=x"}, {"/law/7", 301, "", "/law/7/"},
		{"/decree/2/article/%d8%a7/", 200, "escaped", ""}, {"/absent/", 404, "missing", ""},
		{"/search/?q=labour", 404, "missing", ""}, {"/_export/redirects.json", 404, "missing", ""},
		{"/%2e%2e/etc/passwd", 404, "missing", ""},
	} {
		t.Run(c.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", c.path, nil))
			if w.Code != c.status || w.Body.String() != c.body || w.Header().Get("Location") != c.location {
				t.Fatalf("got %d %q %q", w.Code, w.Body.String(), w.Header().Get("Location"))
			}
			for k, v := range securityHeaders {
				if w.Header().Get(k) != v {
					t.Errorf("header %s differs", k)
				}
			}
		})
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("HEAD", "/", nil))
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD must not send a body")
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatal("POST must be rejected")
	}
}

func TestWorkerSecurityHeaders(t *testing.T) {
	b, err := os.ReadFile("../../workers/static-site/src/headers.js")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range securityHeaders {
		want := `"` + k + `": "` + v + `"`
		if k == "Content-Security-Policy" {
			want = `export const CSP = "` + v + `";`
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("Worker header differs: %s", k)
		}
	}
}
func TestRedirectURIComponent(t *testing.T) {
	if got := encodeURIComponent("3/1 +!()"); got != "3%2F1%20%2B!()" {
		t.Fatal(got)
	}
	d := RedirectData{}
	d.index()
	got := d.Resolve("/law/7/article/%D9%A3%2F1%20%2B!()/", func(p string) bool { return p == "/law/7/article/3%2F1%20%2B!()/" })
	if got != "/law/7/article/3%2F1%20%2B!()/" {
		t.Fatal(got)
	}
}
