// Command check-rulings checks the ruling files, their readings and the reading spend.
//
//	check-rulings [-db dsn] [-dir dir] [-budget usd]
package main

import (
	"errors"
	"flag"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/rulingfile"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	dsn := flag.String("db", cli.DefaultDSN(), "Postgres connection string")
	dir := flag.String("dir", "manual/rulings", "ruling files and reading directory")
	pdfDir := flag.String("pdfs", "", "directory holding the <id>.pdf files (default: -dir)")
	archiveDir := flag.String("archive", "archive", "archived source pages")
	since := flag.Duration("since", 26*time.Hour, "recent-reading window")
	budget := flag.Float64("budget", 250, "maximum combined ruling API spend in USD")
	flag.Parse()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Printf("check-rulings: %v", err)
		os.Exit(2)
	}
	defer pool.Close()
	if err := rulingfile.Check(ctx, pool, *archiveDir, rulingfile.CheckOptions{Dir: *dir, PDFDir: *pdfDir, Since: *since, Budget: *budget}, os.Stdout); err != nil {
		log.Printf("check-rulings: %v", err)
		var failed *rulingfile.CheckFailure
		if errors.As(err, &failed) {
			os.Exit(1)
		}
		os.Exit(2)
	}
}
