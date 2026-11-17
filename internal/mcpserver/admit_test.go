package mcpserver

import (
	"context"
	"fmt"
	"github.com/taktekhq/lebanese-legislation/internal/servertiming"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// heldDB counts the queries in flight and holds each until release closes.
type heldDB struct {
	inflight, peak atomic.Int64
	release        chan struct{}
}

func (d *heldDB) hold(ctx context.Context) {
	n := d.inflight.Add(1)
	for {
		p := d.peak.Load()
		if n <= p || d.peak.CompareAndSwap(p, n) {
			break
		}
	}
	select {
	case <-ctx.Done():
	case <-d.release:
	}
	d.inflight.Add(-1)
}

func (d *heldDB) Query(ctx context.Context, _ string, _ ...any) (pgx.Rows, error) {
	d.hold(ctx)
	return nil, errNoDB
}

func (d *heldDB) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	d.hold(ctx)
	return errRow{}
}

func callFrom(s *Server, ip, body string) int {
	req := httptest.NewRequest("POST", "https://lebaneselegislation.com/mcp", strings.NewReader(body))
	req.Header.Set("CF-Connecting-IP", ip)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec.Code
}

// A burst of searches never puts more queries on the pool than the cap,
// and the calls past it are answered 503 after the short wait.
func TestToolCallsShareADBCap(t *testing.T) {
	db := &heldDB{release: make(chan struct{})}
	s := newServer(db, Config{})
	const n = 20
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = callFrom(s, "203.0.113.7", fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"search_legislation","arguments":{"query":"word%d"}}}`, i, i))
		}()
	}
	time.Sleep(callWait + 500*time.Millisecond)
	if p := db.peak.Load(); p > defaultCallUnits || p == 0 {
		t.Fatalf("peak queries in flight %d, cap %d", p, defaultCallUnits)
	}
	close(db.release)
	wg.Wait()
	busy := 0
	for _, c := range codes {
		if c == 503 {
			busy++
		}
	}
	if busy == 0 || busy == n {
		t.Fatalf("status codes %v: want some served and the rest 503", codes)
	}
	// With room again, a call is served.
	if c := callFrom(s, "198.51.100.9", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_legislation","arguments":{"query":"other"}}}`); c == 503 {
		t.Fatal("cap not released")
	}
}

func TestOneKindSearchWeighsOne(t *testing.T) {
	all := map[string]any{"name": "search_legislation", "arguments": map[string]any{"query": "x"}}
	one := map[string]any{"name": "search_legislation", "arguments": map[string]any{"query": "x", "kind": "laws"}}
	other := map[string]any{"name": "get_law", "arguments": map[string]any{"id": 1}}
	if callWeight(all, 4) != 4 || callWeight(one, 4) != 1 || callWeight(other, 4) != 1 || callWeight(all, 2) != 2 {
		t.Fatal("weights")
	}
}

func TestCallCapOffWhenNegative(t *testing.T) {
	if s := newServer(&heldDB{}, Config{}); s.callUnits != 6 {
		t.Fatalf("default cap %d, want 6", s.callUnits)
	}
	if s := newServer(&heldDB{}, Config{CallUnits: -1}); s.calls != nil {
		t.Fatal("cap on")
	}
	if c := ConfigFromEnv(func(k string) string { return map[string]string{"MCP_CALL_UNITS": "8"}[k] }); c.CallUnits != 8 {
		t.Fatal("MCP_CALL_UNITS not read")
	}
}

// The per-IP limit ignores X-Forwarded-For, which any client can set.
func TestRateLimitIPIgnoresForwardedFor(t *testing.T) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.RemoteAddr = "192.0.2.1:5555"
	r.Header.Set("X-Forwarded-For", "10.9.8.7")
	if got := rateLimitIP(r); got != "192.0.2.1" {
		t.Fatalf("got %q", got)
	}
	r.Header.Set("CF-Connecting-IP", "203.0.113.5")
	if got := rateLimitIP(r); got != "203.0.113.5" {
		t.Fatalf("got %q", got)
	}
}

func timingOf(h http.Handler, body string) string {
	req := httptest.NewRequest("POST", "https://lebaneselegislation.com/mcp", strings.NewReader(body))
	req.Header.Set("CF-Connecting-IP", "203.0.113.9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result().Header.Get("Server-Timing")
}

// Server-Timing names the JSON-RPC method and a tool from our fixed list,
// never what the caller typed.
func TestServerTimingNamesMethodAndTool(t *testing.T) {
	h := servertiming.Middleware(newServer(&heldDB{release: make(chan struct{})}, Config{}))
	if got := timingOf(h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); !strings.Contains(got, `rpc;desc="tools/list"`) || strings.Contains(got, "tool;") || strings.Contains(got, "gate;") {
		t.Errorf("tools/list: %q", got)
	}
	if got := timingOf(h, `{"jsonrpc":"2.0","id":1,"method":"my secret method"}`); !strings.Contains(got, `rpc;desc="other"`) {
		t.Errorf("unknown method: %q", got)
	}
	if got := timingOf(h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); !strings.Contains(got, `rpc;desc="notifications/initialized"`) {
		t.Errorf("notification: %q", got)
	}
	got := timingOf(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"drop table","arguments":{}}}`)
	if !strings.Contains(got, `tool;desc="other"`) || !strings.Contains(got, "gate;dur=") || strings.Contains(got, "drop") {
		t.Errorf("unknown tool: %q", got)
	}
}

// A call refused by the gate reports the wait it spent there.
func TestServerTimingGateWait(t *testing.T) {
	db := &heldDB{release: make(chan struct{})}
	defer close(db.release)
	s := newServer(db, Config{CallUnits: 1})
	h := servertiming.Middleware(s)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_law","arguments":{"id":1}}}`
	go callFrom(s, "203.0.113.8", body) // takes the only unit and holds it
	time.Sleep(200 * time.Millisecond)
	got := timingOf(h, body)
	m := regexp.MustCompile(`gate;dur=([0-9.]+)`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("no gate in %q", got)
	}
	if v, _ := strconv.ParseFloat(m[1], 64); v < float64(callWait.Milliseconds())*0.9 {
		t.Fatalf("gate %s ms, want about %d", m[1], callWait.Milliseconds())
	}
}
