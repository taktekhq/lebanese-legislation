// Command mcp-snapshot writes a D1-ready SQL snapshot of the database for the MCP Worker.
//
//	mcp-snapshot [-db dsn] [-out dir]
package main

import (
	"flag"
	"log"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/snapshot"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	// Export the MCP Worker's D1 snapshot: schema.sql plus chunked
	// data_NNNN.sql files, read-only against Postgres. See
	// internal/snapshot and deploy/snapshot.sh (which loads these into
	// Cloudflare D1 and flips the Worker's binding).
	dsn := flag.String("db", cli.DefaultDSN(), "Postgres connection string")
	out := flag.String("out", "snapshot", "directory to write schema.sql, data_*.sql and manifest.json into")
	guard := cli.GuardFlags(flag.CommandLine)
	flag.Parse()
	start := time.Now()
	m, err := snapshot.Run(ctx, snapshot.Options{DSN: *dsn, OutDir: *out, Guard: guard(), Log: func(f string, a ...any) { log.Printf(f, a...) }})
	if err != nil {
		cli.Exit(err)
	}
	log.Printf("snapshot: wrote %d files in %s: %v", len(m.DataFiles), time.Since(start).Round(time.Second), m.Tables)
}
