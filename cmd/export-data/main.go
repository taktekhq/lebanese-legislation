// Command export-data writes the database as sorted text files for git, after the wording guard passes.
//
//	export-data [-db dsn] [-out dir]
package main

import (
	"flag"
	"log"

	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/export"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	dsn := flag.String("db", cli.DefaultDSN(), "Postgres connection string")
	out := flag.String("out", "data", "the data folder: tables go in <out>/tables, schema and counts beside them")
	guard := cli.GuardFlags(flag.CommandLine)
	flag.Parse()
	if _, err := export.Run(ctx, *dsn, *out, guard(), func(f string, a ...any) { log.Printf(f, a...) }); err != nil {
		cli.Exit(err)
	}
}
