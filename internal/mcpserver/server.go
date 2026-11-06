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
	"context"
	_ "embed"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// ServerName and ServerVersion are what initialize reports.
	ServerName    = "lebaneselegislation"
	ServerVersion = "2.0.0"

	maxBody         = 64 * 1024        // request body cap
	callTimeout     = 5 * time.Second  // per tools/call budget
	cacheTTL        = 60 * time.Second // identical tool calls, briefly
	defaultLimitMin = 60               // requests per IP per minute
	mcpPage         = "https://lebaneselegislation.com/mcp"
	setupPage       = "https://lebaneselegislation.com/ai/"
)

// Config holds the optional settings. The zero value serves the tools with
// the per-IP limit on and every analytics stream off.
type Config struct {
	// GA4 Measurement Protocol, one stream per source; a stream is off
	// unless both its measurement ID and its API secret are set. Never point
	// them at the website's own property: machine calls then read as site
	// visitors.
	GAMCPMeasurementID string
	GAMCPAPISecret     string
	GAHFMeasurementID  string
	GAHFAPISecret      string

	// PostHog: the project token (public), the capture host (default the EU
	// cloud) and the secret that salts visitor ids. Off unless the token is set.
	PostHogKey    string
	PostHogHost   string
	PostHogIDSalt string

	// RateLimitPerMinute is the per-IP limit on /mcp (60 when 0, off when
	// negative). Refusals are logged to PostHog about once per IP per minute.
	RateLimitPerMinute int

	// HuggingFaceAPI is the Hugging Face API base (https://huggingface.co/api
	// when empty), for the "last updated" badge and SendHFStats.
	HuggingFaceAPI string

	// HTTPClient sends the analytics and reads Hugging Face (a client with a
	// 10 s timeout when nil).
	HTTPClient *http.Client

	// Now is the clock (time.Now when nil); Logf logs failures (log.Printf
	// when nil).
	Now  func() time.Time
	Logf func(format string, args ...any)
}

// ConfigFromEnv reads the Worker's variable names: GA_MCP_MEASUREMENT_ID,
// GA_MCP_API_SECRET, GA_HF_MEASUREMENT_ID, GA_HF_API_SECRET, POSTHOG_KEY,
// POSTHOG_HOST, POSTHOG_ID_SALT, plus MCP_RATE_LIMIT (requests per IP per
// minute, negative for none). getenv is os.Getenv when nil.
func ConfigFromEnv(getenv func(string) string) Config {
	if getenv == nil {
		getenv = os.Getenv
	}
	c := Config{
		GAMCPMeasurementID: getenv("GA_MCP_MEASUREMENT_ID"),
		GAMCPAPISecret:     getenv("GA_MCP_API_SECRET"),
		GAHFMeasurementID:  getenv("GA_HF_MEASUREMENT_ID"),
		GAHFAPISecret:      getenv("GA_HF_API_SECRET"),
		PostHogKey:         getenv("POSTHOG_KEY"),
		PostHogHost:        getenv("POSTHOG_HOST"),
		PostHogIDSalt:      getenv("POSTHOG_ID_SALT"),
	}
	if v, err := strconv.Atoi(getenv("MCP_RATE_LIMIT")); err == nil {
		c.RateLimitPerMinute = v
	}
	return c
}

// Server is the MCP endpoint and the badges, as an http.Handler.
type Server struct {
	db      querier
	cfg     Config
	client  *http.Client
	stats   *statsCache
	results *resultCache
	limiter *rateLimiter // nil when off
	refusal *rateLimiter
	bg      sync.WaitGroup

	codeCache  codeList
	calcCache  calcIndex
	englishMem memCache
	frenchMem  memCache

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
	s := &Server{db: db, cfg: cfg, client: cfg.HTTPClient, stats: newStatsCache(6 * time.Hour), results: newResultCache(cacheTTL, 2000)}
	if s.client == nil {
		s.client = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.RateLimitPerMinute >= 0 {
		n := cfg.RateLimitPerMinute
		if n == 0 {
			n = defaultLimitMin
		}
		s.limiter = newRateLimiter(n)
		s.refusal = newRateLimiter(1)
	}
	return s
}

// Flush waits for the analytics sends still in flight (at shutdown).
func (s *Server) Flush() { s.bg.Wait() }

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

func (s *Server) mcpStream() gaStream {
	return gaStream{s.cfg.GAMCPMeasurementID, s.cfg.GAMCPAPISecret}
}
func (s *Server) hfStream() gaStream { return gaStream{s.cfg.GAHFMeasurementID, s.cfg.GAHFAPISecret} }

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
// address, else X-Forwarded-For, else the connection's address.
func rateLimitIP(r *http.Request) string {
	if v := r.Header.Get("CF-Connecting-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return v
	}
	return remoteHost(r)
}

// analyticsIP is the visitor address the analytics hash: Cloudflare's
// client address, else the connection's address.
func analyticsIP(r *http.Request) string {
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
		s.logRefusal(r, ip)
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
	if req.id == nil {
		// A notification gets no response body, only the status.
		setCORS(w.Header())
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var events []gaEvent
	rec := callRecord{}
	started := time.Now()
	resp := s.handleRPC(r, req, &events, &rec)
	ua := r.Header.Get("User-Agent")
	for i := range events {
		events[i].params.set("page_location", mcpPage)
		events[i].params.set("page_title", "MCP server")
		events[i].params.set("client_ua", clip(ua, gaMaxValue))
	}
	s.track(s.mcpStream(), r, events)
	if rec.event != "" && s.phOn() {
		visitor := visitorID(s.cfg.PostHogIDSalt, ip, ua)
		props := obj{}
		if ua != "" {
			props.set("$mcp_client_name", ua)
		}
		props.merge(rec.props)
		props.merge(serverProps())
		props.set("$mcp_duration_ms", time.Since(started).Milliseconds())
		props.set("$session_id", mcpSessionID(visitor, s.now().UTC().Format("2006-01-02")))
		props.set("$current_url", mcpPage)
		props.set("$user_agent", ua)
		if c := r.Header.Get("CF-IPCountry"); c != "" {
			props.set("$geoip_country_code", c)
		}
		props.set("jsonrpc_method", req.methodName())
		// Monitoring bots stay countable but out of the people list.
		clientName := ""
		if req.methodName() == "initialize" {
			if v, has := rec.props.get("$mcp_client_name"); has {
				clientName, _ = v.(string)
			}
		}
		kind := clientKind(ua, clientName)
		props.set("client_kind", kind)
		if kind == "monitor" {
			props.set("$process_person_profile", false)
		}
		s.phCapture(r, []phEvent{{event: rec.event, properties: props, distinctID: visitor}})
	}
	writeJSON(w, http.StatusOK, resp, nil)
}

func serverProps() obj {
	return obj{{"$mcp_server_name", ServerName}, {"$mcp_server_version", ServerVersion}, {"$mcp_source", "lebaneselegislation_worker"}}
}

// logRefusal sends PostHog an mcp_rate_limited event for a request the
// per-IP limit refused, about one per IP per minute, so we see who hits the
// limit without a flood of refusals becoming a flood of events.
func (s *Server) logRefusal(r *http.Request, ip string) {
	if !s.phOn() || !s.refusal.allow(ip) {
		return
	}
	ua := r.Header.Get("User-Agent")
	props := serverProps()
	props.set("$current_url", mcpPage)
	props.set("$user_agent", ua)
	if c := r.Header.Get("CF-IPCountry"); c != "" {
		props.set("$geoip_country_code", c)
	}
	kind := clientKind(ua, "")
	props.set("client_kind", kind)
	props.set("limit_per_minute", s.limiter.perMinute)
	if kind == "monitor" {
		props.set("$process_person_profile", false)
	}
	s.phCapture(r, []phEvent{{event: "mcp_rate_limited", properties: props, distinctID: visitorID(s.cfg.PostHogIDSalt, ip, ua)}})
}

// mcpSessionID groups one machine's calls on one UTC day into a session: a
// UUIDv7 (PostHog's session id shape) whose time is the day's start and
// whose random bits come from the visitor id.
func mcpSessionID(visitor, day string) string {
	t, _ := time.Parse("2006-01-02", day)
	ms := strconv.FormatInt(t.UnixMilli(), 16)
	for len(ms) < 12 {
		ms = "0" + ms
	}
	h := visitor
	for len(h) < 32 {
		h += "0"
	}
	nib, _ := strconv.ParseUint(h[16:17], 16, 8)
	variant := strconv.FormatUint((nib&0x3)|0x8, 16)
	return ms[0:8] + "-" + ms[8:12] + "-7" + h[0:3] + "-" + variant + h[17:20] + "-" + h[20:32]
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

// mcpErrorType buckets a failed tools/call the way PostHog's MCP analytics
// does; tool-level errors (bad arguments, not found) are "validation" and
// set where they happen.
func mcpErrorType(err error) string {
	if errors.Is(err, errTimeout) || errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timed out") {
		return "timeout"
	}
	return "internal"
}
