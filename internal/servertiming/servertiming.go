// Package servertiming adds a Server-Timing header to every response: how
// long the request spent inside this process before its headers went out, so
// the Worker can tell server work from the Cloudflare and Durable Object hop
// around it. It carries no ids, query text or addresses: the MCP method and
// tool name come from fixed sets, the rest are numbers.
package servertiming

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// T is one request's extra timings, filled in by handlers below the
// middleware through From.
type T struct {
	mu   sync.Mutex
	rpc  string
	tool string
	gate time.Duration
	gset bool
}

type ctxKey struct{}

// From returns the request's T, or nil when the middleware is not in front.
// A nil *T is safe to call.
func From(ctx context.Context) *T {
	t, _ := ctx.Value(ctxKey{}).(*T)
	return t
}

// SetRPC records the JSON-RPC method and, for tools/call, the tool name. The
// caller passes only values from fixed sets (otherwise "other").
func (t *T) SetRPC(method, tool string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.rpc, t.tool = method, tool
	t.mu.Unlock()
}

// SetGate records how long a call waited for room in the admission gate.
func (t *T) SetGate(d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.gate, t.gset = d, true
	t.mu.Unlock()
}

// Middleware sets Server-Timing on every response of next.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := &T{}
		tw := &writer{ResponseWriter: w, start: time.Now(), t: t}
		next.ServeHTTP(tw, r.WithContext(context.WithValue(r.Context(), ctxKey{}, t)))
		tw.stamp() // a handler that never wrote: net/http sends the header map after we return
	})
}

type writer struct {
	http.ResponseWriter
	start   time.Time
	t       *T
	stamped bool
}

func (w *writer) stamp() {
	if w.stamped {
		return
	}
	w.stamped = true
	w.ResponseWriter.Header().Set("Server-Timing", w.t.header(time.Since(w.start), load1()))
}

func (w *writer) WriteHeader(code int) {
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(code) // informational: the final header is still to come
		return
	}
	w.stamp()
	w.ResponseWriter.WriteHeader(code)
}

func (w *writer) Write(b []byte) (int, error) {
	w.stamp()
	return w.ResponseWriter.Write(b)
}

func (w *writer) Flush() {
	w.stamp()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the real writer.
func (w *writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func ms(d time.Duration) string {
	return strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 1, 64)
}

func (t *T) header(app time.Duration, load string) string {
	var b strings.Builder
	b.WriteString("app;dur=" + ms(app))
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.gset {
		b.WriteString(", gate;dur=" + ms(t.gate))
	}
	if t.rpc != "" {
		b.WriteString(", rpc;desc=\"" + t.rpc + "\"")
	}
	if t.tool != "" {
		b.WriteString(", tool;desc=\"" + t.tool + "\"")
	}
	if load != "" {
		b.WriteString(", load;desc=\"" + load + "\"")
	}
	return b.String()
}

// The 1-minute load average, read from /proc/loadavg at most once a second.
var (
	loadMu   sync.Mutex
	loadAt   time.Time
	loadLast string
	loadPath = "/proc/loadavg"
)

func load1() string {
	loadMu.Lock()
	defer loadMu.Unlock()
	if !loadAt.IsZero() && time.Since(loadAt) < time.Second {
		return loadLast
	}
	loadAt = time.Now()
	loadLast = ""
	if b, err := os.ReadFile(loadPath); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil && v >= 0 {
				loadLast = strconv.FormatFloat(v, 'f', 2, 64)
			}
		}
	}
	return loadLast
}
