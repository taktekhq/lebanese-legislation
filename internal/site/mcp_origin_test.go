package site

import (
	"net/http/httptest"
	"testing"
)

func TestMCPOriginRuleMatchesThePythonServer(t *testing.T) {
	t.Setenv("MCP_ALLOWED_ORIGINS", "https://app.example, https://other.example/")
	for _, c := range []struct {
		origin, host string
		ok           bool
	}{
		{"", "mcp.example", true},                         // desktop and server clients send no Origin
		{"https://app.example", "mcp.example", true},      // listed
		{"https://other.example", "mcp.example", true},    // listed with a trailing slash
		{"https://evil.example", "mcp.example", false},    // not listed
		{"http://localhost:8001", "localhost:8001", true}, // the same local address
		{"http://localhost:9999", "localhost:8001", false},
		{"https://localhost:8001", "localhost:8001", false}, // scheme differs
		{"http://localhost:8001/x", "localhost:8001", false},
		{"http://user@localhost:8001", "localhost:8001", false},
	} {
		r := httptest.NewRequest("POST", "http://"+c.host+"/mcp", nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := mcpOriginAllowed(r); got != c.ok {
			t.Errorf("origin %q to %s: allowed=%v, want %v", c.origin, c.host, got, c.ok)
		}
	}
}

func TestMCPCORSNamesTheOriginOrRefuses(t *testing.T) {
	t.Setenv("MCP_ALLOWED_ORIGINS", "https://app.example")
	r := httptest.NewRequest("OPTIONS", "/mcp", nil)
	r.Header.Set("Origin", "https://app.example")
	w := httptest.NewRecorder()
	if !mcpCORS(w, r) || w.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatalf("allowed origin: %v", w.Header())
	}
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	if mcpCORS(w, r) || w.Code != 403 {
		t.Fatalf("refused origin: code %d", w.Code)
	}
}

func TestLawOutlineIsWholeUnlessPaged(t *testing.T) {
	law := func() *apiLawDetail {
		out := &apiLawDetail{}
		for i := 0; i < 1044; i++ {
			out.Articles = append(out.Articles, apiArticleRef{})
		}
		return out
	}
	full := law()
	pageLawArticles(full, 0, 0)
	if len(full.Articles) != 1044 {
		t.Fatalf("default: %d articles, want all 1044", len(full.Articles))
	}
	page := law()
	pageLawArticles(page, 1000, 100)
	if len(page.Articles) != 44 {
		t.Fatalf("last page: %d", len(page.Articles))
	}
	past := law()
	pageLawArticles(past, 5000, 10)
	if len(past.Articles) != 0 {
		t.Fatalf("past the end: %d", len(past.Articles))
	}
}
