// Package mcpserver is the Lebanese Legislation MCP server: the Model
// Context Protocol over Streamable HTTP (stateless JSON-RPC 2.0 POSTs) with
// ten read-only tools over the corpus in Postgres, and the live stat badges
// for the Hugging Face dataset card.
//
// The hosted endpoint is https://lebaneselegislation.com/mcp, routed by
// Cloudflare to the live origin container. All tools read Postgres directly.
// Nothing here writes.
package mcpserver

import (
	"bytes"
	_ "embed"
	"errors"
	"github.com/taktekhq/lebanese-legislation/internal/servertiming"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/internal/memo"
	"github.com/taktekhq/lebanese-legislation/internal/wordsearch"
	"golang.org/x/sync/semaphore"
)

const (
	// ServerName and ServerVersion are what initialize reports.
	ServerName    = "lebaneselegislation"
	ServerVersion = "2.0.0"

	maxBody         = 64 * 1024        // request body cap
	callTimeout     = 5 * time.Second  // per tools/call budget
	cacheTTL        = 60 * time.Second // identical tool calls, briefly
	defaultLimitMin = 60               // requests per IP per minute
	setupPage       = "https://lebaneselegislation.com/ai/"
)

// Config holds the optional settings. The zero value serves the tools with
// the per-IP limit on.
type Config struct {
	// RateLimitPerMinute is the per-IP limit on /mcp (60 when 0, off when
	// negative).
	RateLimitPerMinute int

	// CallUnits caps the database work of tools/call in flight across all
	// clients, in queries (6 when 0, off when negative). A search over every
	// kind counts 4. A call that finds no room within 2 s gets a 503.
	CallUnits int

	// SearchIndex is the file of search_legislation's word lists
	// (wordsearch.Index; "lebleg-web search-index" writes it, the -all
	// image has it); "" searches with the GIN indexes alone. The server maps
	// it read-only in the background and searches without it until it is
	// ready (about 0.1 s), or for good if it does not match the database.
	SearchIndex string

	// HuggingFaceAPI is the Hugging Face API base (https://huggingface.co/api
	// when empty), for the "last updated" and downloads badges.
	HuggingFaceAPI string

	// HTTPClient sends the analytics and reads Hugging Face (a client with a
	// 10 s timeout when nil).
	HTTPClient *http.Client

	// Now is the clock (time.Now when nil); Logf logs failures (log.Printf
	// when nil).
	Now  func() time.Time
	Logf func(format string, args ...any)
}

// ConfigFromEnv reads MCP_RATE_LIMIT (requests per IP per minute, negative
// for none) and MCP_CALL_UNITS (CallUnits). getenv is os.Getenv when nil.
func ConfigFromEnv(getenv func(string) string) Config {
	if getenv == nil {
		getenv = os.Getenv
	}
	c := Config{}
	if v, err := strconv.Atoi(getenv("MCP_RATE_LIMIT")); err == nil {
		c.RateLimitPerMinute = v
	}
	if v, err := strconv.Atoi(getenv("MCP_CALL_UNITS")); err == nil {
		c.CallUnits = v
	}
	c.SearchIndex = getenv("MCP_SEARCH_INDEX")
	return c
}

// Server is the MCP endpoint and the badges, as an http.Handler.
type Server struct {
	db        querier
	cfg       Config
	client    *http.Client
	results   *resultCache
	limiter   *rateLimiter        // nil when off
	calls     *semaphore.Weighted // tools/call DB work in flight; nil when off
	callUnits int

	counts memo.Map // the paged tools' totals (count.go)

	layout      atomic.Pointer[layout]           // the database's layout, once known (db.go sql)
	searchKnown atomic.Pointer[wordsearch.Known] // read once for the searches (search_pg.go known)
	searchIndex *indexLoad                       // the word lists' load, nil for none (search_pg.go)
	pubCount    atomic.Pointer[bool]             // law_pub_count is there, once known (count.go pubCounts)

	badgeMu    sync.Mutex
	badgeAt    time.Time
	badgeStats badgeStats

	hfMu          sync.Mutex
	hfModified    string
	hfModifiedAt  time.Time
	hfDownloadsN  int
	hfDownloadsAt time.Time
}

// New serves the corpus behind pool. Connect it read-only: the server
// never writes (options='-c default_transaction_read_only=on').
func New(pool *pgxpool.Pool, cfg Config) *Server { return newServer(pool, cfg) }

func newServer(db querier, cfg Config) *Server {
	s := &Server{db: db, cfg: cfg, client: cfg.HTTPClient, results: newResultCache(cacheTTL, 2000)}
	if s.client == nil {
		s.client = &http.Client{Timeout: 10 * time.Second}
	}
	s.calls, s.callUnits = newCallSem(cfg.CallUnits)
	if cfg.SearchIndex != "" {
		s.searchIndex = loadIndex(s, cfg.SearchIndex)
	}
	if cfg.RateLimitPerMinute >= 0 {
		n := cfg.RateLimitPerMinute
		if n == 0 {
			n = defaultLimitMin
		}
		s.limiter = newRateLimiter(n)
	}
	return s
}

func (s *Server) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

func (s *Server) today() string { return s.now().UTC().Format("2006-01-02") }

func (s *Server) logf(format string, args ...any) {
	if s.cfg.Logf != nil {
		s.cfg.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func setCORS(h http.Header) {
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id, Mcp-Protocol-Version, Authorization")
	h.Set("Access-Control-Expose-Headers", "Mcp-Session-Id, Mcp-Protocol-Version")
}

func writeJSON(w http.ResponseWriter, status int, body any, extra map[string]string) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Mcp-Protocol-Version", defaultProtocol)
	setCORS(h)
	for k, v := range extra {
		h.Set(k, v)
	}
	b, err := marshal(body)
	if err != nil {
		b = []byte(`{"error":"internal error"}`)
	}
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// rateLimitIP is the key the per-IP limit counts by: Cloudflare's client
// address, else the connection's address. X-Forwarded-For is not used: a
// client sets it freely, so a new value each time would be a new bucket.
func rateLimitIP(r *http.Request) string {
	if v := r.Header.Get("CF-Connecting-IP"); v != "" {
		return v
	}
	return remoteHost(r)
}

func remoteHost(r *http.Request) string {
	if r.RemoteAddr == "" {
		return "unknown"
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// ServeHTTP answers /mcp (and /mcp/), /badge/<name>.svg, and 404s anything else.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		setCORS(w.Header())
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/badge/") {
		s.serveBadge(w, r)
		return
	}
	if r.URL.Path != "/mcp" && r.URL.Path != "/mcp/" {
		writeJSON(w, http.StatusNotFound, obj{{"error", "not found"}, {"hint", "this Worker only serves /mcp"}}, nil)
		return
	}
	if r.Method == http.MethodGet {
		// A person who clicks the address in a post gets the setup page, not
		// JSON; MCP clients probing for SSE send Accept: text/event-stream.
		accept := r.Header.Get("Accept")
		if strings.Contains(accept, "text/html") && !strings.Contains(accept, "text/event-stream") {
			w.Header().Set("Location", setupPage)
			w.WriteHeader(http.StatusFound)
			return
		}
		writeJSON(w, http.StatusMethodNotAllowed, obj{
			{"error", "this MCP server is stateless: POST a JSON-RPC 2.0 request, there is no SSE stream to open"},
			{"see", setupPage},
		}, map[string]string{"Allow": "POST, OPTIONS"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, obj{{"error", "method not allowed"}}, map[string]string{"Allow": "POST, OPTIONS"})
		return
	}

	ip := rateLimitIP(r)
	if s.limiter != nil && !s.limiter.allow(ip) {
		writeJSON(w, http.StatusTooManyRequests, obj{{"error", "too many requests, slow down"}}, map[string]string{"Retry-After": "2"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, rpcError(nil, rpcParseError, "invalid JSON"), nil)
		return
	}
	if len(body) > maxBody {
		writeJSON(w, http.StatusRequestEntityTooLarge, obj{{"error", "request body too large"}}, nil)
		return
	}
	body = bytes.TrimPrefix(body, []byte("\uFEFF"))
	req, perr := parseRequest(body)
	if perr != nil {
		writeJSON(w, http.StatusBadRequest, rpcError(nil, rpcParseError, "invalid JSON"), nil)
		return
	}
	if !req.valid() {
		writeJSON(w, http.StatusBadRequest, rpcError(req.id, rpcInvalidRequest, "not a JSON-RPC 2.0 request"), nil)
		return
	}
	tm := servertiming.From(r.Context())
	tm.SetRPC(timingMethod(req.methodName()), timingTool(req))
	if req.id == nil {
		// A notification gets no response body, only the status.
		setCORS(w.Header())
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if req.methodName() == "tools/call" {
		waited := time.Now()
		release, ok := s.admitCall(r.Context(), objectParams(req.params))
		tm.SetGate(time.Since(waited))
		if !ok {
			s.logf("mcp tools/call: busy, refused %s", ip)
			writeJSON(w, http.StatusServiceUnavailable, rpcError(req.id, rpcServerBusy, "the server is busy, try again shortly"), map[string]string{"Retry-After": "2"})
			return
		}
		defer release()
	}

	resp := s.handleRPC(r, req)
	writeJSON(w, http.StatusOK, resp, nil)
}

// ---------------------------------------------------------------- rate limiting

// rateLimiter is a token bucket per key: perMinute tokens, refilled
// continuously.
type rateLimiter struct {
	mu        sync.Mutex
	perMinute int
	buckets   map[string]*bucket
	now       func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(perMinute int) *rateLimiter {
	return &rateLimiter{perMinute: perMinute, buckets: map[string]*bucket{}, now: time.Now}
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	perSec := float64(l.perMinute) / 60
	b, has := l.buckets[key]
	if !has {
		if len(l.buckets) > 100_000 {
			// forget the buckets that have refilled
			for k, v := range l.buckets {
				if now.Sub(v.last) > time.Minute {
					delete(l.buckets, k)
				}
			}
		}
		b = &bucket{tokens: float64(l.perMinute), last: now}
		l.buckets[key] = b
	} else {
		b.tokens = min(float64(l.perMinute), b.tokens+now.Sub(b.last).Seconds()*perSec)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// ---------------------------------------------------------------- result cache

// resultCache keeps successful tool answers for a minute: identical calls
// in a burst (a client retrying, a page of clients) read the corpus once.
type resultCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	entries map[string]cachedResult
}

type cachedResult struct {
	text    string
	count   any
	expires time.Time
}

func newResultCache(ttl time.Duration, max int) *resultCache {
	return &resultCache{ttl: ttl, max: max, entries: map[string]cachedResult{}}
}

func (c *resultCache) get(key string) (cachedResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, has := c.entries[key]
	if !has || time.Now().After(e.expires) {
		return cachedResult{}, false
	}
	return e, true
}

func (c *resultCache) put(key, text string, count any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if len(c.entries) >= c.max {
		for k, e := range c.entries {
			if now.After(e.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= c.max {
			c.entries = map[string]cachedResult{}
		}
	}
	c.entries[key] = cachedResult{text: text, count: count, expires: now.Add(c.ttl)}
}

// errTimeout is a tools/call that ran past its budget.
var errTimeout = errors.New("tool call timed out")
