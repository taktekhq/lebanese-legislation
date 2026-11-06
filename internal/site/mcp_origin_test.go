package site

import (
	"encoding/json"
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
			out.Articles = append(out.Articles, apiArticleRef{Position: i + 1})
		}
		return out
	}
	full := law()
	if next := pageLawArticles(full, 0, 0); len(full.Articles) != 1044 || next != -1 {
		t.Fatalf("default: %d articles next %d, want all 1044", len(full.Articles), next)
	}
	page := law()
	if next := pageLawArticles(page, 900, 100); len(page.Articles) != 100 || page.Articles[0].Position != 901 || next != 1000 {
		t.Fatalf("a middle page: %d from %d, next %d", len(page.Articles), page.Articles[0].Position, next)
	}
	last := law()
	if next := pageLawArticles(last, 1000, 100); len(last.Articles) != 44 || next != -1 {
		t.Fatalf("last page: %d next %d", len(last.Articles), next)
	}
	exact := law()
	if next := pageLawArticles(exact, 944, 100); len(exact.Articles) != 100 || next != -1 {
		t.Fatalf("a last page that is full: %d next %d", len(exact.Articles), next)
	}
	past := law()
	if next := pageLawArticles(past, 5000, 10); len(past.Articles) != 0 || next != -1 {
		t.Fatalf("past the end: %d next %d", len(past.Articles), next)
	}
}

// get_law pages its outline by articles_cursor; articles_offset is refused
// with what to pass instead, before anything is read.
func TestGetLawArticlesOffsetRemoved(t *testing.T) {
	s := &Server{}
	for raw, want := range map[string]string{
		`{"id": 1, "articles_offset": 100}`:       articlesOffsetRemoved,
		`{"id": 1, "articles_offset": 0}`:         articlesOffsetRemoved,
		`{"id": 1, "articles_cursor": "nope"}`:    "articles_cursor is not one this server made; pass articles_next_cursor unchanged",
		`{"id": 1, "articles_cursor": 5}`:         "articles_cursor must be a string and articles_limit a non-negative integer",
		`{"id": 1, "articles_limit": -1}`:         "articles_cursor must be a string and articles_limit a non-negative integer",
		`{"id": 1, "articles_cursor": "djF8LTE"}`: "articles_cursor is not one this server made; pass articles_next_cursor unchanged", // "v1|-1"
	} {
		res, isErr, err := s.toolGetLaw(t.Context(), json.RawMessage(raw))
		if err != nil || !isErr || res.(map[string]any)["error"] != want {
			t.Errorf("%s: %v %v %v, want %q", raw, res, isErr, err, want)
		}
	}
}

func TestRateLimitOffAllowsEverything(t *testing.T) {
	off := newIPLimiter(0, 20)
	for i := 0; i < 1000; i++ {
		if _, ok := off.Allow("192.0.2.1"); !ok {
			t.Fatalf("request %d refused with the limit off", i)
		}
	}
	on := newIPLimiter(60, 5)
	refused := 0
	for i := 0; i < 20; i++ {
		if _, ok := on.Allow("192.0.2.1"); !ok {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("a 60 a minute limit with a burst of 5 must refuse some of 20 quick requests")
	}
}
