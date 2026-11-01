// Command static-site writes the website as static files, rendered by the same server code.
//
//	static-site [-db dsn] [-out dir] [-base path] [-workers n]
package main

import (
	"flag"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/static"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	dsn := flag.String("db", cli.DefaultDSN(), "Postgres connection string")
	out := flag.String("out", "pages", "directory to write the site into")
	base := flag.String("base", "", "path the site is served under (\"\" for a domain root)")
	workers := flag.Int("workers", 4, "pages rendered in parallel")
	flag.Parse()
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
