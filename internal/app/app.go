// Package app runs one of the three public services from the same code:
// the website, the REST API or the MCP server. web/, api/ and mcp/ each
// build a binary that picks its mode; APP_MODE overrides it.
package app

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
	"github.com/taktekhq/lebanese-legislation/internal/semantic"
	"github.com/taktekhq/lebanese-legislation/internal/site"
)

// Defaults for optional semantic search, used only when GEMINI_API_KEY is set.
const (
	defaultSearchModel = "gemini-3.8-flash"
	defaultDailyCalls  = 500
)

// Main runs mode, or with the single argument "healthcheck" asks the running
// service for /healthz (the images have no shell or curl).
func Main(mode string) {
	if m := os.Getenv("APP_MODE"); m != "" {
		mode = m
	}
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := healthcheck(listenAddr(mode)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Getenv("DATABASE_URL"), mode); err != nil {
		log.Fatal(err)
	}
}

func run(databaseURL, mode string) error {
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return errors.New("invalid database configuration")
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return errors.New("database unavailable")
	}
	handler, err := site.New(pool)
	if err != nil {
		return err
	}
	if err = enableSemanticSearch(handler, pool); err != nil {
		return err
	}
	runtimeHandler, err := modeHandler(handler, mode)
	if err != nil {
		return err
	}
	addr := listenAddr(mode)
	if base := os.Getenv("PUBLIC_URL"); base != "" {
		site.Base = base
	}
	srv := &http.Server{Addr: addr, Handler: runtimeHandler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdown)
		case <-done:
		}
	}()
	defer close(done)
	log.Printf("%s listening on %s", mode, addr)
	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
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
	rewriter, err := nlsearch.New(nlsearch.Config{Provider: "gemini", Model: model, Key: key})
	if err != nil {
		return err
	}
	s.SetMeaningSearch(rewriter, "Google Gemini", false, daily)
	embedder, err := semantic.NewGemini(key, os.Getenv("GEMINI_EMBEDDING_MODEL"))
	if err != nil {
		return errors.New("invalid embedding configuration")
	}
	s.SetVectorSearch(semantic.NewStore(pool, embedder))
	log.Printf("semantic search enabled: %s, at most %d calls a day", model, daily)
	return nil
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
// everything, including /api/v1/ and /mcp, as LebaneseLegislation.com does.
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
