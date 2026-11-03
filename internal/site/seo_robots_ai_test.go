package site

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// AI crawlers (ShapBot included) may read search, the API and the MCP
// server; search engines ("*") stay out of /api/ and /search/.
func TestRobotsLetsAICrawlersReadTheAPI(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/robots.txt", nil))
	body := rec.Body.String()
	for _, bot := range []string{"GPTBot", "ClaudeBot", "Claude-SearchBot", "PerplexityBot", "ShapBot"} {
		_, group, ok := strings.Cut(body, "User-agent: "+bot+"\n")
		if !ok {
			t.Errorf("robots.txt has no %s group", bot)
			continue
		}
		group, _, _ = strings.Cut(group, "\n\n")
		if !strings.Contains(group, "Allow: /") || strings.Contains(group, "Disallow: /api/") || strings.Contains(group, "Disallow: /search/") || strings.Contains(group, "Disallow: /*?") {
			t.Errorf("%s group = %q", bot, group)
		}
		if !strings.Contains(group, "Disallow: /changes/review/") {
			t.Errorf("%s may read the internal review pages", bot)
		}
	}
	if !strings.Contains(body, "User-agent: *\nAllow: /\nDisallow: /search/\nDisallow: /en/search/\nDisallow: /api/") {
		t.Error("the search engines' group changed")
	}
}
