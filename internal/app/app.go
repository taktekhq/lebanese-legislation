// Package app runs one of the three services from the same code: the
// website, the REST API or the MCP server. web/, api/ and mcp/ each build a
// binary that picks its mode; APP_MODE overrides it.
//
// The binary's defaults are LebaneseLegislation.com's own (canonical links
// to it, 60 API and MCP requests a minute per client, its browser
// analytics). The published images set the local defaults instead
// (PUBLIC_URL, no rate limit, ANALYTICS=off).
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/internal/mcpserver"
	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
	"github.com/taktekhq/lebanese-legislation/internal/semantic"
	"github.com/taktekhq/lebanese-legislation/internal/site"
	"github.com/taktekhq/lebanese-legislation/internal/vectorize"
)

// Defaults for semantic search set up from GEMINI_API_KEY alone.
const (
	defaultSearchModel = "gemini-3.8-flash"
	defaultDailyCalls  = 4000 // about 2,000 AI answers, about $9 a day
)

// config is the command line. Every flag can be left out.
type config struct {
	mode        string
	dsn         string // -db, or DATABASE_URL, or LEBLEG_DSN
	addr        string // -addr, or LISTEN_ADDR, or the mode's usual port
	authFile    string // -auth: a file holding user:password; every page asks for it
	noindex     bool   // -noindex: keep the site out of search engines
	events      string // -events: folder for the anonymous usage record
	scans       string // -scans: page images of texts read from Gazette scans
	official    string // -official: page images and PDFs of texts from official scans
	rulingFiles string // -ruling-files: the judgment PDFs, served at /ruling-files/<id>.pdf
}

// Main runs mode. With the single argument "healthcheck" it asks the running
// service for /healthz instead (the images have no shell or curl).
func Main(mode string) {
	log.SetFlags(0)
	if m := os.Getenv("APP_MODE"); m != "" {
		mode = m
	}
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "healthcheck" {
		if err := healthcheck(listenAddr(mode)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	cfg, err := parseFlags(mode, args, os.Stderr)
	if err != nil {
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}

// parseFlags reads the command line. A leading "serve" is skipped, so the
// command line of the former single binary ("serve -addr ... -db ...")
// still works unchanged.
func parseFlags(mode string, args []string, stderr io.Writer) (config, error) {
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	cfg := config{mode: mode}
	fs := flag.NewFlagSet(mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("LEBLEG_DSN")
	}
	fs.StringVar(&cfg.dsn, "db", dsn, "PostgreSQL connection string (default $DATABASE_URL, then $LEBLEG_DSN)")
	fs.StringVar(&cfg.addr, "addr", listenAddr(mode), "address to listen on (default $LISTEN_ADDR, then the mode's port on loopback)")
	fs.StringVar(&cfg.authFile, "auth", "", "preview mode: a file holding user:password; every page then asks for it and is marked noindex")
	fs.BoolVar(&cfg.noindex, "noindex", false, "keep the site out of search engines")
	fs.StringVar(&cfg.events, "events", "", "keep the anonymous usage record (searches, reads, AI sign-ups) in this folder")
	fs.StringVar(&cfg.scans, "scans", "", "page images of texts read from Gazette scans")
	fs.StringVar(&cfg.official, "official", "", "folder of page images and PDFs for texts published from official scans")
	fs.StringVar(&cfg.rulingFiles, "ruling-files", "", "folder of judgment PDFs, served at /ruling-files/<id>.pdf (empty: none)")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return config{}, errors.New("unexpected arguments")
	}
	return cfg, nil
}

func run(ctx context.Context, cfg config) error {
	if cfg.dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	if _, err := modeHandler(nil, cfg.mode); err != nil {
		return err
	}
	pgcfg, err := pgxpool.ParseConfig(cfg.dsn)
	if err != nil {
		return errors.New("invalid database configuration")
	}
	pgcfg.MaxConns = 8
	// JIT compiles each search's plan; on these short queries that costs more
	// than it saves (a two-digit search took 903 ms with it, 134 ms without).
	pgcfg.ConnConfig.RuntimeParams["jit"] = "off"
	pool, err := pgxpool.NewWithConfig(ctx, pgcfg)
	if err != nil {
		return errors.New("invalid database configuration")
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return errors.New("database unavailable")
	}
	if base := os.Getenv("PUBLIC_URL"); base != "" {
		site.Base = strings.TrimRight(base, "/")
	}
	site.OfficialDir = cfg.official
	h, err := site.New(pool)
	if err != nil {
		return err
	}
	h.SetScans(cfg.scans)
	h.SetRulingFiles(cfg.rulingFiles)
	// Server errors to PostHog's error tracking when POSTHOG_KEY is set. The
	// browser analytics (LebaneseLegislation.com's own projects) are on
	// unless ANALYTICS=off; the published images set it.
	h.SetPostHog(os.Getenv("POSTHOG_KEY"), os.Getenv("POSTHOG_HOST"))
	h.SetAnalytics(os.Getenv("ANALYTICS") != "off")
	closeVectors, err := enableSearch(ctx, h, pool)
	if err != nil {
		return err
	}
	defer closeVectors()
	perMinute, err := envInt("RATE_LIMIT_PER_MINUTE", 60, 0, 100000)
	if err != nil {
		return err
	}
	burst, err := envInt("RATE_LIMIT_BURST", 20, 1, 10000)
	if err != nil {
		return err
	}
	h.SetRateLimit(perMinute, burst)
	if cfg.events != "" {
		ev, err := site.OpenEvents(cfg.events)
		if err != nil {
			return err
		}
		h.SetEvents(ev)
	}
	go h.WarmSitemaps(ctx) // Build the big sitemap files before a crawler needs them.
	handler, err := modeHandler(site.Compress(h), cfg.mode)
	if err != nil {
		return err
	}
	// The origin shares the canonical pool with the ten-tool MCP server.
	if os.Getenv("MCP_SHARED_SERVER") == "1" {
		mcp := mcpserver.New(pool, mcpserver.ConfigFromEnv(nil))
		defer mcp.Flush()
		handler = withMCP(handler, mcp)
	}
	if cfg.noindex {
		handler = site.NoIndex(handler)
	}
	if cfg.authFile != "" {
		b, err := os.ReadFile(cfg.authFile)
		if err != nil {
			return err
		}
		user, pass, ok := strings.Cut(strings.TrimSpace(string(b)), ":")
		if !ok || user == "" || pass == "" {
			return errors.New("auth file must hold user:password")
		}
		handler = site.Private(handler, user, pass)
		log.Print("preview mode: password required, noindex")
	}
	return serve(ctx, cfg.addr, cfg.mode, handler)
}

// serve listens until ctx ends, then lets the requests in hand finish (for up
// to 8 seconds, inside Docker's 10 second stop timeout).
func serve(ctx context.Context, addr, mode string, handler http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if srv.Shutdown(sctx) != nil {
			srv.Close()
		}
	}()
	if mode == "" {
		mode = "website"
	}
	log.Printf("%s listening on http://%s/", mode, addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-done
	return nil
}

// enableSearch turns on search by meaning. LEBLEG_SEARCH_PROVIDER picks the
// provider (openai, gemini or claude) and LEBLEG_VECTOR_DSN a separate
// database for the passage vectors; without them, GEMINI_API_KEY alone turns
// on Gemini with the vectors in the main database (semantic_chunks).
// Without either, exact and citation search still work. The returned func
// closes what it opened.
func enableSearch(ctx context.Context, h *site.Server, pool *pgxpool.Pool) (func(), error) {
	if os.Getenv("LEBLEG_SEARCH_PROVIDER") == "" && os.Getenv("LEBLEG_VECTOR_DSN") == "" {
		return func() {}, enableSemanticSearch(h, pool)
	}
	if os.Getenv("LEBLEG_SEARCH_PROVIDER") == "" && os.Getenv("LEBLEG_VECTORIZE_INDEX") != "" {
		// A separate Vectorize metadata DSN must not disable automatic Gemini answers.
		if err := enableSemanticSearch(h, pool); err != nil {
			return nil, err
		}
	} else if err := enableProviderSearch(h); err != nil {
		return nil, err
	}
	dsn := os.Getenv("LEBLEG_VECTOR_DSN")
	if dsn == "" || os.Getenv("GEMINI_API_KEY") == "" {
		return func() {}, nil
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid vector database configuration")
	}
	cfg.MaxConns = 2
	vectors, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("cannot configure vector database")
	}
	model := os.Getenv("LEBLEG_EMBEDDING_MODEL")
	if model == "" {
		model = semantic.DefaultModel
	}
	embedder, err := semantic.NewGemini(os.Getenv("GEMINI_API_KEY"), model)
	if err != nil {
		vectors.Close()
		return nil, errors.New("invalid embedding configuration")
	}
	store, err := semanticStore(vectors, embedder)
	if err != nil {
		vectors.Close()
		return nil, err
	}
	h.SetVectorSearch(store)
	log.Printf("semantic retrieval enabled: %s", model)
	return vectors.Close, nil
}

// enableProviderSearch reads LEBLEG_SEARCH_PROVIDER, LEBLEG_SEARCH_MODEL,
// LEBLEG_SEARCH_SELECT_MODEL, LEBLEG_SEARCH_DAILY_LIMIT (1-10000, default
// 500) and LEBLEG_SEARCH_SHARES_DATA, and the provider's own API key.
func enableProviderSearch(h *site.Server) error {
	provider := os.Getenv("LEBLEG_SEARCH_PROVIDER")
	if provider == "" {
		return nil
	}
	keyVar := map[string]string{"openai": "OPENAI_API_KEY", "gemini": "GEMINI_API_KEY", "claude": "ANTHROPIC_API_KEY"}[provider]
	labels := map[string]string{"openai": "OpenAI", "gemini": "Google Gemini", "claude": "Anthropic Claude"}
	client, err := nlsearch.New(nlsearch.Config{Provider: provider, Model: os.Getenv("LEBLEG_SEARCH_MODEL"),
		SelectModel: os.Getenv("LEBLEG_SEARCH_SELECT_MODEL"), Key: os.Getenv(keyVar)})
	if err != nil {
		return err
	}
	daily := defaultDailyCalls
	if raw := os.Getenv("LEBLEG_SEARCH_DAILY_LIMIT"); raw != "" {
		daily, err = strconv.Atoi(raw)
		if err != nil || daily < 1 || daily > 10000 {
			return errors.New("LEBLEG_SEARCH_DAILY_LIMIT must be 1-10000")
		}
	}
	// OpenAI may keep what it is sent unless LEBLEG_SEARCH_SHARES_DATA=0 says
	// the account does not; the search form names this.
	shares := os.Getenv("LEBLEG_SEARCH_SHARES_DATA") == "1" || (provider == "openai" && os.Getenv("LEBLEG_SEARCH_SHARES_DATA") != "0")
	h.SetMeaningSearch(client, labels[provider], shares, daily)
	log.Printf("search by meaning: %s, %s, at most %d API calls a day", provider, os.Getenv("LEBLEG_SEARCH_MODEL"), daily)
	return nil
}

// enableSemanticSearch turns on search by meaning when GEMINI_API_KEY is set:
// Gemini rewrites the question and embeds it, and PostgreSQL compares it with
// the saved passage vectors in semantic_chunks. Without a key, exact and
// citation search still work.
func enableSemanticSearch(s *site.Server, pool *pgxpool.Pool) error {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		return nil
	}
	model := os.Getenv("GEMINI_SEARCH_MODEL")
	if model == "" {
		model = defaultSearchModel
	}
	daily := defaultDailyCalls
	if raw := os.Getenv("GEMINI_MAX_CALLS_PER_DAY"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 10000 {
			return errors.New("GEMINI_MAX_CALLS_PER_DAY must be 0-10000")
		}
		if n == 0 {
			log.Print("search by meaning off: GEMINI_MAX_CALLS_PER_DAY is 0")
			return nil
		}
		daily = n
	}
	concurrency, err := envInt("GEMINI_CONCURRENCY", 2, 1, 16)
	if err != nil {
		return err
	}
	cacheSize, err := envInt("GEMINI_QUERY_CACHE_SIZE", 256, 0, 256)
	if err != nil {
		return err
	}
	cacheTTL, err := envInt("GEMINI_QUERY_CACHE_TTL", 3600, 0, 3600)
	if err != nil {
		return err
	}
	go probeOutbound()
	rewriter, err := nlsearch.New(nlsearch.Config{Provider: "gemini", Model: model, Key: key})
	if err != nil {
		return err
	}
	s.SetMeaningSearch(rewriter, "Google Gemini", false, daily)
	s.SetMeaningLimits(concurrency, cacheSize, time.Duration(cacheTTL)*time.Second)
	embedder, err := semantic.NewGemini(key, os.Getenv("GEMINI_EMBEDDING_MODEL"))
	if err != nil {
		return errors.New("invalid embedding configuration")
	}
	store, err := semanticStore(pool, embedder)
	if err != nil {
		return err
	}
	s.SetVectorSearch(store)
	log.Printf("semantic search enabled: %s, at most %d calls a day", model, daily)
	return nil
}

// envInt reads an integer setting within [low, high], or def when it is unset.
func envInt(name string, def, low, high int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < low || n > high {
		return 0, fmt.Errorf("%s must be %d-%d", name, low, high)
	}
	return n, nil
}

// listenAddr is LISTEN_ADDR, or the mode's usual port on loopback.
func listenAddr(mode string) string {
	if addr := os.Getenv("LISTEN_ADDR"); addr != "" {
		return addr
	}
	if mode == "api" {
		return "127.0.0.1:8000"
	}
	return "127.0.0.1:8080"
}

func healthcheck(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", addr, err)
	}
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: %s", resp.Status)
	}
	return nil
}

// modeHandler exposes only the routes a mode serves. The website serves
// everything, including /api/v1/ and /mcp.
func modeHandler(next http.Handler, mode string) (http.Handler, error) {
	switch mode {
	case "", "website":
		return next, nil
	case "api":
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/api/v1/") {
				next.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		}), nil
	case "mcp":
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/mcp", "/mcp/", "/healthz":
				next.ServeHTTP(w, r)
			default:
				http.NotFound(w, r)
			}
		}), nil
	default:
		return nil, errors.New("APP_MODE must be website, api or mcp")
	}
}

// semanticStore keeps the local ANN path unless Vectorize is explicitly selected.
func semanticStore(pool *pgxpool.Pool, embedder semantic.Embedder) (*semantic.Store, error) {
	store := semantic.NewStore(pool, embedder)
	if index := os.Getenv("LEBLEG_VECTORIZE_INDEX"); index != "" {
		client, err := vectorize.New(os.Getenv("CLOUDFLARE_ACCOUNT_ID"), index, os.Getenv("LEBLEG_VECTORIZE_TOKEN"))
		if err != nil {
			return nil, err
		}
		store.WithVectorize(client)
	}
	return store, nil
}

// probeOutbound logs once whether the server can reach the HTTPS services it
// depends on, so a blocked or broken outbound path shows in the start log.
func probeOutbound() {
	client := &http.Client{Timeout: 10 * time.Second}
	for _, u := range []string{"https://generativelanguage.googleapis.com/", "https://api.cloudflare.com/client/v4/"} {
		resp, err := client.Get(u)
		if err != nil {
			log.Printf("outbound probe %s: %v", u, err)
			continue
		}
		_ = resp.Body.Close()
		log.Printf("outbound probe %s: HTTP %d", u, resp.StatusCode)
	}
}

func withMCP(next, mcp http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" || r.URL.Path == "/mcp/" || strings.HasPrefix(r.URL.Path, "/badge/") {
			mcp.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
