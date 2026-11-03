package site

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// ShapBot may read search, the API and the MCP server; everyone else is kept
// out of /api/ and /search/.
func TestRobotsLetsShapBotReadTheAPI(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/robots.txt", nil))
	body := rec.Body.String()
	_, group, ok := strings.Cut(body, "User-agent: ShapBot\n")
	if !ok {
		t.Fatal("robots.txt has no ShapBot group")
	}
	group, _, _ = strings.Cut(group, "\n\n")
	if !strings.Contains(group, "Allow: /") || strings.Contains(group, "Disallow: /api/") || strings.Contains(group, "Disallow: /search/") {
		t.Errorf("ShapBot group = %q", group)
	}
	if !strings.Contains(body, "User-agent: *\nAllow: /\nDisallow: /search/") {
		t.Error("the default group changed")
	}
}
