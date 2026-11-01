// Command static-site writes the website as static files, for a host such
// as GitHub Pages: every page rendered by the same code as the live site, in
// its static mode (internal/static).
//
//	static-site -db "host=/tmp dbname=lebleg" -out ../lebaneselegislation-pages -base /lebaneselegislation
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/static"
)

func main() {
	log.SetFlags(0)
	dsn := flag.String("db", defaultDSN(), "PostgreSQL connection string (default $DATABASE_URL, then $LEBLEG_DSN)")
	out := flag.String("out", "../lebaneselegislation-pages", "directory to write the site into")
	base := flag.String("base", "/lebaneselegislation", `path the site is served under ("" for a domain root)`)
	workers := flag.Int("workers", 4, "pages rendered in parallel")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("set -db, DATABASE_URL or LEBLEG_DSN")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := pgxpool.ParseConfig(*dsn)
	if err != nil {
		log.Fatal(err)
	}
	cfg.MaxConns = int32(*workers) + 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	start := time.Now()
	st, err := static.Build(ctx, pool, *out, *base, *workers, func(f string, a ...any) { log.Printf(f, a...) })
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %d pages, %.0f MB, in %s", st.Pages, float64(st.Bytes)/1e6, time.Since(start).Round(time.Second))
}

func defaultDSN() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	return os.Getenv("LEBLEG_DSN")
}
