package site

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A UI string is trusted markup; what it is formatted with is not. t escapes
// strings and Stringers, keeps template.HTML (safe links, nested t) and
// leaves numbers to their verbs.
func TestTEscapesItsArguments(t *testing.T) {
	type stringer struct{ time.Duration }
	got := escArgs([]any{`<b>"x"</b>`, template.HTML(`<a href="/">y</a>`), 42, 3 * time.Second, stringer{time.Second}})
	want := []any{"&lt;b&gt;&#34;x&#34;&lt;/b&gt;", `<a href="/">y</a>`, 42, "3s", "1s"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d: %#v, want %#v", i, got[i], want[i])
		}
	}
	for name, funcs := range map[string]template.FuncMap{"en": baseFuncs, "ar": arabicFuncs()} {
		tr := funcs["t"].(func(string, ...any) template.HTML)
		if out := string(tr("search.nothing", "<b>x</b>")); strings.Contains(out, "<b>") || !strings.Contains(out, "&lt;b&gt;x&lt;/b&gt;") {
			t.Errorf("%s t search.nothing: %s", name, out)
		}
		if out := string(tr("footer.credit", template.HTML(`<a class="link" href="https://taktek.io/">Taktek, LLC</a>`))); !strings.Contains(out, `<a class="link" href="https://taktek.io/">Taktek, LLC</a>`) {
			t.Errorf("%s t footer.credit lost its link: %s", name, out)
		}
		if out := string(tr("annex.fine", "1,000", "2,000")); !strings.Contains(out, "1,000") || !strings.Contains(out, "2,000") {
			t.Errorf("%s t annex.fine: %s", name, out)
		}
	}
}

// The no-match heading of a search shows the visitor's words as text.
func TestSearchNothingIsEscaped(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	const q = "<b>x</b>"
	s.scache.put(q, Results{Q: q}, "") // what searchFor answers when nothing matches
	for _, path := range []string{"/search/", "/en/search/"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path+"?q="+url.QueryEscape(q), nil))
		body := rec.Body.String()
		if rec.Code != 200 || strings.Contains(body, q) || !strings.Contains(body, "&lt;b&gt;x&lt;/b&gt;") {
			t.Errorf("GET %s: status %d, raw markup in the page or escaped form missing", path, rec.Code)
		}
	}
}

// An outside link in a UI string escapes its address and drops one that is
// not a web address.
func TestExtLink(t *testing.T) {
	if got := extLink(`https://a.example/?x=1&y="2"`, "text"); got != `<a class="link" href="https://a.example/?x=1&amp;y=&#34;2&#34;" rel="noopener">text</a>` {
		t.Errorf("https link: %s", got)
	}
	if got := extLink("javascript:alert(1)", "text"); got != "text" {
		t.Errorf("javascript link: %s", got)
	}
}

// No redirect ever leaves the site: a path that starts with two slashes (or
// a slash and a backslash) is a path here, and /en with an escaped slash
// after it does not crash the trailing-slash rule.
func TestRedirectsStayOnSite(t *testing.T) {
	s := noDBServer(t)
	for _, c := range []struct{ raw, to string }{
		{"//evil.example/x", "/evil.example/x/"},
		{"//evil.example/x?a=1", "/evil.example/x/?a=1"},
		{"///evil.example/x", "/evil.example/x/"},
		{"/ar//evil.example/x", "/evil.example/x/"},
		{"/en%2F%2Fevil", "/en/evil/"},
		{"/en%2F%2Fevil.example%2Fx", "/en/evil.example/x/"},
	} {
		for _, method := range []string{"GET", "HEAD"} {
			req := httptest.NewRequest(method, "/", nil)
			u, err := url.ParseRequestURI(c.raw)
			if err != nil {
				t.Fatal(err)
			}
			req.URL, req.RequestURI = u, c.raw
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			loc := rec.Header().Get("Location")
			if rec.Code != http.StatusMovedPermanently || loc != c.to {
				t.Errorf("%s %s: %d Location %q, want 301 to %q", method, c.raw, rec.Code, loc, c.to)
			}
			if strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, `/\`) {
				t.Errorf("%s %s: Location %q leaves the site", method, c.raw, loc)
			}
		}
	}
	for _, p := range []string{"//evil.example/x", `/\evil.example/x`, "%2F%2Fx", ""} {
		if to := s.finalPath(t.Context(), p); strings.HasPrefix(to, "//") || strings.HasPrefix(to, `/\`) || !strings.HasPrefix(to, "/") {
			t.Errorf("finalPath(%q) = %q", p, to)
		}
	}
	rec := httptest.NewRecorder()
	movedTo(rec, httptest.NewRequest("GET", "/", nil), `/\evil.example/`)
	if loc := rec.Header().Get("Location"); loc != "/evil.example/" {
		t.Errorf("movedTo /\\evil.example/: Location %q", loc)
	}
}

// UL pictures: only names of the shape UL uses, and only raster images.
func TestULImageNames(t *testing.T) {
	for _, ok := range []string{"1046.gif", "11883_10-1.png", "1072(1).png", "13927-ة2-_Page_1.png", "dir/a.jpg"} {
		if !ulImageName.MatchString(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "%2e%2e/x.png", "a%2fb.png", "../x.png", "a/../x.png", ".x.png", "a//b.png", `a\b.png`, "a b.png", "a?.png", "/a.png", "a/"} {
		if ulImageName.MatchString(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ct := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
		if !ulImageTypes[ct] {
			t.Errorf("%s not relayed", ct)
		}
	}
	for _, ct := range []string{"image/svg+xml", "text/html", "image/x-icon", "application/octet-stream"} {
		if ulImageTypes[ct] {
			t.Errorf("%s relayed", ct)
		}
	}
	s := noDBServer(t)
	for _, p := range []string{"/ul/uploads/images/%252e%252e/x.png", "/ul/uploads/images/a%25b.png"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404 before any fetch", p, rec.Code)
		}
	}
}

// A scan's old file URLs send a reader only to an HTTPS address at a scan
// archive.
func TestScanSourceURL(t *testing.T) {
	for _, ok := range []string{"https://saderlex.com/gazette/pdf/14588", "https://www.saderlex.com/x", "https://SaderLex.com/x"} {
		if !scanSourceURL(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"http://saderlex.com/x", "https://evil.example/x", "//saderlex.com/x", "javascript:alert(1)", "https://saderlex.com.evil.example/x", "https://u@saderlex.com/x", "https://saderlex.com:8443/x", "/scan/1/"} {
		if scanSourceURL(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

// Official files are served only when they are regular files.
func TestOfficialFilesRegularOnly(t *testing.T) {
	dir := t.TempDir()
	old := OfficialDir
	OfficialDir = dir
	t.Cleanup(func() { OfficialDir = old })
	if err := os.MkdirAll(filepath.Join(dir, "5", "dir.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "5", "page-1.png"), []byte("\x89PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "5", "link.png")); err != nil {
		t.Fatal(err)
	}
	s := noDBServer(t)
	for path, want := range map[string]int{
		"/official-files/5/page-1.png": 200,
		"/official-files/5/dir.png":    404,
		"/official-files/5/link.png":   404,
		"/official-files/5/none.png":   404,
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("GET %s: %d, want %d", path, rec.Code, want)
		}
	}
}
