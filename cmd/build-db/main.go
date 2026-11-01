// Command build-db builds the database from archive/ from scratch and reports what it loaded.
//
//	build-db [-db dsn] [-archive dir] [-workers n] [-only step]
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/taktekhq/lebanese-legislation/internal/archive"
	"github.com/taktekhq/lebanese-legislation/internal/build"
	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/modelout"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	dsn := flag.String("db", cli.DefaultDSN(), "Postgres connection string")
	dir := flag.String("archive", "archive", "the git-tracked archive directory")
	workers := flag.Int("workers", 4, "parallel parsers (keep low on a small machine)")
	only := flag.String("only", "", `rerun one step on the database as it is ("amendments", "graph", "ruling-files", "scan-gazette", "indexing")`)
	citations := flag.String("citations", "db/model/citations.jsonl", "the model's citations (the cite command)")
	rulingCitations := flag.String("ruling-citations", modelout.DefaultRulingCitations, "the model's reading of rulings, comma-separated files: the texts each applies, cites or interprets (citeagy -source rulings)")
	scans := flag.String("scans", "", "texts the Centre has not published yet, read from Gazette scans (a lebgazette sader-bundle folder)")
	rulingFiles := flag.String("ruling-files", "", "the folder ruling-files fills: judgment PDFs' index and the machine readings (empty: skip)")
	flag.Parse()
	rep, err := build.Run(ctx, build.Options{Archive: archive.Archive{Dir: *dir}, DSN: *dsn,
		Workers: *workers, Only: *only, Citations: *citations, RulingCitations: *rulingCitations, Scans: *scans, RulingFiles: *rulingFiles, Log: func(f string, a ...any) { log.Printf(f, a...) }})
	if rep != nil {
		fmt.Println()
		rep.Write(os.Stdout)
	}
	if err != nil {
		log.Fatal(err)
	}
	if *only == "" && !rep.OK() {
		os.Exit(2)
	}
}
