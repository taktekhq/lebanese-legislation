package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/web/internal/site"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(os.Getenv("DATABASE_URL")); err != nil {
		log.Fatal(err)
	}
}
func run(databaseURL string) error {
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
	runtimeHandler, err := modeHandler(handler, os.Getenv("APP_MODE"))
	if err != nil {
		return err
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
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
	log.Printf("Listening on %s", addr)
	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// modeHandler shares the MCP implementation while restricting the standalone worker.
func modeHandler(next http.Handler, mode string) (http.Handler, error) {
	switch mode {
	case "", "website":
		return next, nil
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
		return nil, errors.New("APP_MODE must be website or mcp")
	}
}
