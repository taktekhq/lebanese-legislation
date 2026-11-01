// Command load-official loads texts published from official Gazette scans into the database.
//
//	load-official -bundles dir [-db dsn] [-files dir]
package main

import (
	"flag"
	"log"

	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/official"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	dsn := flag.String("db", cli.DefaultDSN(), "Postgres connection string")
	bundles := flag.String("bundles", "", "folder written by lebgazette sader-bundle")
	files := flag.String("files", "official-files", "where page images and PDFs are copied (serve -official)")
	flag.Parse()
	if *bundles == "" {
		log.Fatal("load-official needs -bundles")
	}
	if err := official.Load(ctx, *dsn, *bundles, *files, log.Printf); err != nil {
		log.Fatal(err)
	}
}
