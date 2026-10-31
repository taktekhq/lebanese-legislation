package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDatabaseURLRequired(t *testing.T) {
	if err := run(""); err == nil {
		t.Fatal("empty database URL must fail before starting a listener")
	}
}

func TestMCPModeRestrictsRoutes(t *testing.T) {
	called := 0
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++; w.WriteHeader(204) })
	handler, err := modeHandler(upstream, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/mcp", "/mcp/", "/healthz"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 204 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/", "/api/v1/search/", "/mcp/extra", "/en/mcp"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	if called != 3 {
		t.Fatalf("forwarded %d requests", called)
	}
	if _, err := modeHandler(upstream, "typo"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}
