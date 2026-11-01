// Command check-release says whether a database holds one verified,
// restored release: a restore receipt matching the release row, PostgreSQL
// 18.6 and pgvector 0.8.2, and, with -manifest, the very release that file
// names. The db image's health check runs it. Exit 1 when it does not.
//
//	check-release -db dsn [-manifest file]
package main

import (
	"flag"

	"github.com/taktekhq/lebanese-legislation/internal/release"
	"github.com/taktekhq/lebanese-legislation/internal/releasedb"
)

func main() {
	ctx, stop := release.Context()
	defer stop()
	dsn := flag.String("db", release.Env("DATABASE_URL", ""), "the database (default DATABASE_URL)")
	manifest := flag.String("manifest", release.Env("LEBLEG_MANIFEST_FILE", ""), "a local manifest the restored release must match")
	flag.Parse()
	release.Exit(releasedb.CheckReceipt(ctx, *dsn, *manifest))
}
