// Command calculated-current writes the source-verified calculated wording of each article as JSONL.
//
//	calculated-current [-db dsn] [-out file]
package main

import (
	"flag"
	"log"

	"github.com/taktekhq/lebanese-legislation/internal/calculatedcurrent"
	"github.com/taktekhq/lebanese-legislation/internal/cli"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	dsn := flag.String("db", cli.DefaultDSN(), "Postgres connection string")
	out := flag.String("out", "data/article_calculated_current.jsonl", "derived JSONL output file, or - for stdout")
	guard := cli.GuardFlags(flag.CommandLine)
	flag.Parse()
	n, err := calculatedcurrent.Export(ctx, *dsn, *out, guard())
	if err != nil {
		cli.Exit(err)
	}
	log.Printf("calculated-current: wrote %d source-verified rows to %s", n, *out)
}
