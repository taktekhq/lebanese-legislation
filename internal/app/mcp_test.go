package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/mcpserver"
)

func TestCombinedServerMCP(t *testing.T) {
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) })
	h := withMCP(fallback, mcpserver.New(nil, mcpserver.Config{RateLimitPerMinute: -1}))
	for _, path := range []string{"/mcp", "/mcp/"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var response struct {
			Result struct{ Tools []struct{ Name string } }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(response.Result.Tools) != 10 {
			t.Fatalf("%s: status=%d tools=%d body=%s", path, w.Code, len(response.Result.Tools), w.Body.String())
		}
	}
	for _, path := range []string{"/", "/api/v1/", "/mcp-unrelated"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 299 {
			t.Fatalf("website route %s intercepted: %d", path, w.Code)
		}
	}
}
