package site

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSyntheticServing checks the public serving boundaries against only the
// fictional records in testdata/synthetic.sql. It never needs a corpus release.
func TestSyntheticServing(t *testing.T) {
	dsn := os.Getenv("DEMO_DATABASE_URL")
	if dsn == "" {
		t.Skip("DEMO_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, want string }{
		{"/en/", "Lebanese Legislation"},
		{"/law/1/", "نص تجريبي"},
		{"/law/1/article/1/", "المكتبة العامة"},
		{"/search/?q=المكتبة", "المكتبة"},
		{"/api/v1/search/?q=المكتبة", `"law_count":1`},
		{"/api/v1/law/1/", `"id":1`},
		{"/api/v1/law/1/article/1/", "المكتبة العامة"},
		{"/sitemap.xml", "sitemap"},
		{"/styles.css", "body"},
		{"/contribute/", "ساهم"},
		{"/en/contribute/", "Improve search and code"},
		{"/sitemaps/core.xml", "/en/contribute/"},
		{"/sitemaps/ar-core.xml", "/contribute/"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("GET %s: %d, missing %q; body %.700s", tc.path, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_legislation","arguments":{"query":"المكتبة","limit":5}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_law","arguments":{"id":1}}}`,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		s.ServeHTTP(rec, req)
		var result struct {
			Result json.RawMessage
			Error  json.RawMessage
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || len(result.Error) > 0 || len(result.Result) == 0 || strings.Contains(rec.Body.String(), `"isError":true`) {
			t.Fatalf("MCP %s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
}
