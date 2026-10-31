package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDatabaseURLRequired(t *testing.T) {
	if err := run("", "website"); err == nil {
		t.Fatal("empty database URL must fail before starting a listener")
	}
}

func modeRoutes(t *testing.T, mode string, allowed, refused []string) {
	t.Helper()
	called := 0
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++; w.WriteHeader(204) })
	handler, err := modeHandler(upstream, mode)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range allowed {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 204 {
			t.Fatalf("%s %s: %d", mode, path, w.Code)
		}
	}
	for _, path := range refused {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("%s %s: %d, want 404", mode, path, w.Code)
		}
	}
	if called != len(allowed) {
		t.Fatalf("%s forwarded %d requests, want %d", mode, called, len(allowed))
	}
}

func TestMCPModeRestrictsRoutes(t *testing.T) {
	modeRoutes(t, "mcp", []string{"/mcp", "/mcp/", "/healthz"}, []string{"/", "/api/v1/search/", "/mcp/extra", "/en/mcp"})
}

func TestAPIModeRestrictsRoutes(t *testing.T) {
	modeRoutes(t, "api", []string{"/api/v1/", "/api/v1/search/", "/api/v1/law/1/", "/healthz"}, []string{"/", "/mcp", "/law/1/", "/en/", "/api/v2/"})
}

func TestWebsiteModeServesEverything(t *testing.T) {
	modeRoutes(t, "website", []string{"/", "/mcp", "/api/v1/search/", "/law/1/"}, nil)
	if _, err := modeHandler(nil, "typo"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestListenAddrDefaultsPerMode(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	if got := listenAddr("api"); got != "127.0.0.1:8000" {
		t.Fatalf("api: %s", got)
	}
	if got := listenAddr("mcp"); got != "127.0.0.1:8080" {
		t.Fatalf("mcp: %s", got)
	}
	t.Setenv("LISTEN_ADDR", "0.0.0.0:9000")
	if got := listenAddr("website"); got != "0.0.0.0:9000" {
		t.Fatalf("override: %s", got)
	}
}

func TestHealthcheckAsksTheLocalPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	if err := healthcheck("0.0.0.0:" + port); err != nil {
		t.Fatal(err)
	}
	if err := healthcheck("0.0.0.0:1"); err == nil {
		t.Fatal("a closed port must fail")
	}
}

func TestSemanticSearchNeedsAKeyOnly(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	if err := enableSemanticSearch(nil, nil); err != nil {
		t.Fatal("no key must leave search exact-only, not fail")
	}
	t.Setenv("GEMINI_API_KEY", "test-only")
	t.Setenv("GEMINI_MAX_CALLS_PER_DAY", "0")
	if err := enableSemanticSearch(nil, nil); err == nil {
		t.Fatal("a zero daily cap must be refused")
	}
}
