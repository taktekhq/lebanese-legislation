package servertiming

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"
)

func get(h http.Handler) *http.Response {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	return rec.Result()
}

func durOf(t *testing.T, header string) float64 {
	t.Helper()
	m := regexp.MustCompile(`^app;dur=([0-9.]+)`).FindStringSubmatch(header)
	if m == nil {
		t.Fatalf("no app;dur in %q", header)
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v
}

func TestHeaderOnWriteHeader(t *testing.T) {
	res := get(Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(204)
		time.Sleep(60 * time.Millisecond) // after the header: must not count
	})))
	d := durOf(t, res.Header.Get("Server-Timing"))
	if d < 20 || d > 55 {
		t.Fatalf("dur %.1f ms, want 20 to 55", d)
	}
}

func TestHeaderOnImplicitWrite(t *testing.T) {
	res := get(Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hi"))
	})))
	if res.Header.Get("Server-Timing") == "" || res.StatusCode != 200 {
		t.Fatalf("header %q status %d", res.Header.Get("Server-Timing"), res.StatusCode)
	}
}

func TestHeaderWhenHandlerNeverWrites(t *testing.T) {
	// A real server, since the recorder reads headers early.
	srv := httptest.NewServer(Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	defer srv.Close()
	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	durOf(t, res.Header.Get("Server-Timing"))
}

func TestMCPFieldsAndGate(t *testing.T) {
	res := get(Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tm := From(r.Context())
		tm.SetRPC("tools/call", "get_article")
		tm.SetGate(1500 * time.Microsecond)
		w.WriteHeader(200)
	})))
	h := res.Header.Get("Server-Timing")
	for _, want := range []string{`gate;dur=1.5`, `rpc;desc="tools/call"`, `tool;desc="get_article"`} {
		if !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(h) {
			t.Errorf("%q missing from %q", want, h)
		}
	}
}

func TestNoMCPFieldsOnPlainRequest(t *testing.T) {
	h := get(Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))).Header.Get("Server-Timing")
	for _, no := range []string{"gate", "rpc", "tool"} {
		if regexp.MustCompile(no).MatchString(h) {
			t.Errorf("%s in %q", no, h)
		}
	}
}

func TestNilTimingIsSafe(t *testing.T) {
	var tm *T = From(httptest.NewRequest("GET", "/", nil).Context())
	tm.SetRPC("a", "b")
	tm.SetGate(time.Second)
}

func TestLoadReadAtMostOncePerSecond(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "loadavg")
	os.WriteFile(p, []byte("0.42 0.30 0.20 1/100 5\n"), 0o600)
	loadPath, loadAt = p, time.Time{}
	if got := load1(); got != "0.42" {
		t.Fatalf("load %q", got)
	}
	os.WriteFile(p, []byte("9.00 0 0 1/1 1\n"), 0o600)
	if got := load1(); got != "0.42" {
		t.Fatalf("re-read within a second: %q", got)
	}
	loadAt = time.Now().Add(-2 * time.Second)
	if got := load1(); got != "9.00" {
		t.Fatalf("after a second: %q", got)
	}
	loadPath, loadAt = filepath.Join(dir, "missing"), time.Time{}
	if got := load1(); got != "" {
		t.Fatalf("missing file: %q", got)
	}
	loadPath, loadAt = "/proc/loadavg", time.Time{}
}
