package site

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIPLimiterBurstThenThrottle(t *testing.T) {
	l := newIPLimiter(60, 5)
	for i := 0; i < 5; i++ {
		if _, ok := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("call %d within burst was throttled", i)
		}
	}
	wait, ok := l.Allow("1.2.3.4")
	if ok {
		t.Fatalf("call past the burst was allowed")
	}
	if wait <= 0 {
		t.Errorf("expected a positive Retry-After, got %v", wait)
	}

	if _, ok := l.Allow("5.6.7.8"); !ok {
		t.Errorf("a different client was throttled by another client's bucket")
	}
}

func TestIPLimiterRefills(t *testing.T) {
	l := newIPLimiter(600, 1)
	if _, ok := l.Allow("1.1.1.1"); !ok {
		t.Fatalf("first call was throttled")
	}
	if _, ok := l.Allow("1.1.1.1"); ok {
		t.Fatalf("second call was allowed with no tokens left")
	}
	time.Sleep(150 * time.Millisecond)
	if _, ok := l.Allow("1.1.1.1"); !ok {
		t.Errorf("call after waiting for refill was still throttled")
	}
}

func TestDBSemaphore(t *testing.T) {
	d := newDBSemaphore(1)
	release1, ok := d.acquire(context.Background(), time.Second)
	if !ok {
		t.Fatalf("first acquire failed")
	}
	start := time.Now()
	_, ok = d.acquire(context.Background(), 50*time.Millisecond)
	if ok {
		t.Fatalf("second acquire should have queued and timed out, not succeeded")
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Errorf("acquire returned before its wait elapsed")
	}
	release1()
	release2, ok := d.acquire(context.Background(), time.Second)
	if !ok {
		t.Fatalf("acquire after release failed")
	}
	release2()
}

func TestTTLCache(t *testing.T) {
	c := newTTLCache(2, 50*time.Millisecond)
	a, b := json.RawMessage(`{"q":"a"}`), json.RawMessage(`{"q":"b"}`)
	c.set("search", a, "A")
	c.set("search", b, "B")
	if v, ok := c.get("search", a); !ok || v != "A" {
		t.Errorf("get a: %v, %v", v, ok)
	}

	c.set("search", json.RawMessage(`{"q":"c"}`), "C")
	if _, ok := c.get("search", a); ok {
		t.Errorf("oldest entry was not evicted")
	}
	if v, ok := c.get("search", b); !ok || v != "B" {
		t.Errorf("get b after eviction: %v, %v", v, ok)
	}
	time.Sleep(60 * time.Millisecond)
	if _, ok := c.get("search", b); ok {
		t.Errorf("expired entry was still served")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/mcp", nil)
	r.Header.Set("CF-Connecting-IP", "203.0.113.9")
	r.RemoteAddr = "10.0.0.1:5555"
	if got := clientIP(r); got != "203.0.113.9" {
		t.Errorf("clientIP with header = %q", got)
	}

	r2 := httptest.NewRequest("GET", "/mcp", nil)
	r2.RemoteAddr = "10.0.0.1:5555"
	if got := clientIP(r2); got != "10.0.0.1" {
		t.Errorf("clientIP without header = %q", got)
	}
}

func TestMCPRateLimitReturns429(t *testing.T) {
	s := testServer(t)
	s.ipLim = newIPLimiter(60, 1)
	req := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		r.RemoteAddr = "9.9.9.9:1"
		rec := httptest.NewRecorder()
		s.mcpPost(rec, r)
		return rec
	}
	if rec := req(); rec.Code != http.StatusOK {
		t.Fatalf("first call: status %d", rec.Code)
	}
	rec := req()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second call: status %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Errorf("429 missing Retry-After")
	}
}
