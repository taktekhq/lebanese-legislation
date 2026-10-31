package site

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func maxAge(cc string) int {
	m := regexp.MustCompile(`(?:^|[ ,])max-age=(\d+)`).FindStringSubmatch(cc)
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func TestProbesAnsweredWithoutAPage(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("User-Agent", "codex-mcp-client/0.163.0-alpha.11")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	for _, p := range []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-authorization-server/mcp",
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
		"/.well-known/openid-configuration",
	} {
		rec := do(p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404 (Location %q)", p, rec.Code, rec.Header().Get("Location"))
		}
		cc := rec.Header().Get("Cache-Control")
		if !strings.Contains(cc, "public") || maxAge(cc) < 3600 {
			t.Errorf("%s: Cache-Control %q, want public and max-age >= 3600", p, cc)
		}
	}
	for _, p := range []string{"/mcp", "/mcp/"} {
		rec := do(p)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: %d, want 405 (Location %q)", p, rec.Code, rec.Header().Get("Location"))
		}
		if a := rec.Header().Get("Allow"); !strings.Contains(a, "POST") {
			t.Errorf("GET %s: Allow %q, want POST named", p, a)
		}
	}
}

func TestSitemapCacheControlPublicHour(t *testing.T) {
	rec := httptest.NewRecorder()
	xmlOut(rec, "<urlset/>")
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "public") || maxAge(cc) < 3600 {
		t.Errorf("sitemap Cache-Control %q, want public and max-age >= 3600", cc)
	}
}

func TestMCPRoutesAfterRoutesDoNotConflict(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("mcpRoutes after routes panicked: %v", p)
		}
	}()
	s.mcpRoutes()
	for _, m := range []string{"GET", "OPTIONS"} {
		for _, p := range []string{"/mcp", "/mcp/"} {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(m, p, nil))
			want := map[string]int{"GET": http.StatusMethodNotAllowed, "OPTIONS": http.StatusNoContent}[m]
			if rec.Code != want {
				t.Errorf("%s %s: %d, want %d", m, p, rec.Code, want)
			}
		}
	}
}
