package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/db"
)

// The DB tests load the real schema and testdata/fixture.sql into a fresh
// schema of the database LEBLEG_TEST_DSN names (a disposable one), and drop
// it afterwards. Without the variable they are skipped.

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcpserver tests:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	dsn := os.Getenv("LEBLEG_TEST_DSN")
	if dsn == "" {
		return m.Run(), nil
	}
	ctx := context.Background()
	schema := fmt.Sprintf("mcpserver_test_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return 0, err
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		return 0, err
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return 0, err
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return 0, err
	}
	defer pool.Close()
	fixture, err := os.ReadFile("testdata/fixture.sql")
	if err != nil {
		return 0, err
	}
	for _, sql := range []string{db.Schema, db.AmendmentSchema, db.GraphSchema, string(fixture)} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			return 0, err
		}
	}
	testPool = pool
	return m.Run(), nil
}

// dbServer is a server over the fixture, or skips the test.
func dbServer(t *testing.T) *Server {
	t.Helper()
	if testPool == nil {
		t.Skip("set LEBLEG_TEST_DSN to a disposable Postgres database")
	}
	return newServer(testPool, Config{RateLimitPerMinute: -1})
}

// noDB is a database that fails every query: for tests that must not
// reach one.
type noDB struct{}

var errNoDB = errors.New("no database in this test")

func (noDB) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, errNoDB }
func (noDB) QueryRow(context.Context, string, ...any) pgx.Row       { return errRow{} }

type errRow struct{}

func (errRow) Scan(...any) error { return errNoDB }

// post sends one JSON-RPC body to h.
func post(h http.Handler, body any, headers map[string]string) *httptest.ResponseRecorder {
	var b []byte
	if s, isStr := body.(string); isStr {
		b = []byte(s)
	} else {
		b, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(http.MethodPost, "https://lebaneselegislation.com/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("CF-Connecting-IP", "203.0.113.1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// callResult is a tools/call answer, decoded.
type callResult struct {
	isError bool
	result  map[string]any // the result, or the error body
	advice  string
}

// call runs a tool through the real JSON-RPC path.
func call(t *testing.T, h http.Handler, name string, args map[string]any) callResult {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	rec := post(h, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}, nil)
	var body struct {
		Error  any `json:"error"`
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("tools/call %s: %v: %s", name, err, rec.Body.String())
	}
	if body.Error != nil {
		t.Fatalf("tools/call %s returned a JSON-RPC error: %v", name, body.Error)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body.Result.Content[0].Text), &parsed); err != nil {
		t.Fatal(err)
	}
	out := callResult{isError: body.Result.IsError, result: parsed}
	if r, has := parsed["result"].(map[string]any); has {
		out.result = r
		out.advice, _ = parsed["not_legal_advice"].(string)
	}
	return out
}

// recorder is an http.RoundTripper that keeps every outbound request and
// answers it itself: the tests make no network calls.
type recorder struct {
	mu     sync.Mutex
	reqs   []recorded
	answer func(*http.Request) *http.Response
}

type recorded struct {
	url  string
	body map[string]any
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]any
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(b, &body)
	}
	r.mu.Lock()
	r.reqs = append(r.reqs, recorded{req.URL.String(), body})
	r.mu.Unlock()
	if r.answer != nil {
		return r.answer(req), nil
	}
	return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}, Request: req}, nil
}

func (r *recorder) sent(prefix string) []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recorded
	for _, q := range r.reqs {
		if strings.HasPrefix(q.url, prefix) {
			out = append(out, q)
		}
	}
	return out
}

func jsonResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}, Request: req}
}
