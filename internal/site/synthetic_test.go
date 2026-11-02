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
		// when the content last changed (page_state), on the page and in its JSON-LD
		{"/law/1/", `<time datetime="2026-10-30">30 تشرين الأول 2026</time>`},
		{"/en/law/1/", `"dateModified":"2026-10-30"`},
		{"/en/law/1/article/1/", `Last updated 30 October 2026`},
		{"/codes/synthetic-library/", `<time datetime="2026-10-30">`},
		{"/en/laws/year/2024/law/", `Last updated 30 October 2026`},
		// and the same date as the API's updated_at
		{"/api/v1/law/1/", `"updated_at":"2026-10-30"`},
		{"/api/v1/law/1/article/1/", `"updated_at":"2026-10-30","not_legal_advice"`},
		{"/api/v1/search/?q=المكتبة&type=laws", `"updated_at":"2026-10-30"`},
		{"/api/v1/laws/", `"updated_at":"2026-10-30"`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("GET %s: %d, missing %q; body %.700s", tc.path, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	// The Gazette issue's page_state date is before the site existed (an old
	// build's seed): no date at all, rather than a false one.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/en/gazette/1/", nil))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "Last updated") || strings.Contains(rec.Body.String(), "dateModified") {
		t.Fatalf("/en/gazette/1/: %d, shows a date it does not have", rec.Code)
	}
	for _, path := range []string{"/api/v1/gazette/1/", "/api/v1/gazette/"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		var issue struct {
			UpdatedAt *string `json:"updated_at"`
			Issues    []struct {
				UpdatedAt *string `json:"updated_at"`
			} `json:"issues"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &issue) != nil || issue.UpdatedAt != nil || (len(issue.Issues) > 0 && issue.Issues[0].UpdatedAt != nil) {
			t.Fatalf("GET %s: %d, an updated_at the issue does not have: %s", path, rec.Code, rec.Body.String())
		}
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
		if strings.Contains(body, "get_law") && !strings.Contains(rec.Body.String(), `updated_at\":\"2026-10-30`) {
			t.Fatalf("MCP get_law: no updated_at: %s", rec.Body.String())
		}
	}
	// Every path /openapi.json describes is a JSON route of the server.
	var spec struct {
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(openapiJSON, &spec); err != nil {
		t.Fatal(err)
	}
	for p := range spec.Paths {
		path := strings.NewReplacer("{id}", "1", "{n}", "1").Replace(p)
		if strings.Contains(p, "calculated-current") {
			path = "/api/v1/law/244565/calculated-current/"
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path+"?q=x", nil)
		req.Header.Set("CF-Connecting-IP", "198.51.100.7") // a client of its own: the earlier requests used up the default one's allowance
		s.ServeHTTP(rec, req)
		if (rec.Code != 200 && rec.Code != 404) || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || !json.Valid(rec.Body.Bytes()) {
			t.Errorf("GET %s: %d %q, not a JSON answer", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}
