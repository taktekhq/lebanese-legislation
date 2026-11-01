// Command restore-release downloads a published database release from the
// Hugging Face dataset (or takes a local manifest and dump), checks its
// SHA-256 and restores it into an empty PostgreSQL 18.6 database with
// pgvector 0.8.2, then checks every count, hash and index, adds what the
// website, API and MCP read (web.sql) and records a restore receipt. It
// never drops data; an interrupted restore of the same release resumes.
// The db image runs it on first start.
//
//	restore-release -db dsn [-manifest dir/manifest.json [-dump file]]
//	                [-repo owner/name] [-revision main] [-manifest-path releases/latest.json]
package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/hfhub"
	"github.com/taktekhq/lebanese-legislation/internal/release"
	"github.com/taktekhq/lebanese-legislation/internal/releasedb"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	dsn := flag.String("db", release.Env("DATABASE_URL", cli.DefaultDSN()), "the empty target database (default DATABASE_URL)")
	manifest := flag.String("manifest", "", "a local manifest.json instead of downloading")
	dump := flag.String("dump", "", "the local dump (default: the manifest's dump path beside it)")
	repo := flag.String("repo", release.DefaultRepo, "the Hugging Face dataset")
	revision := flag.String("revision", "main", "the dataset branch, tag or commit")
	manifestPath := flag.String("manifest-path", release.LatestPath, "a manifest or pointer in the dataset")
	cache := flag.String("cache-dir", ".cache/lebleg", "where downloaded dumps are kept, by dataset commit")
	reader := flag.String("reader-role", release.Env("READER_ROLE", "lebleg_reader"), "an existing restricted role to grant SELECT; empty for none")
	pgRestore := flag.String("pg-restore", release.Env("PG_RESTORE_BIN", "pg_restore"), "the pg_restore 18.6 executable")
	webSQL := flag.String("web-sql", release.Env("LEBLEG_WEB_SQL", "tools/src/lebleg/sql/web.sql"), "the website schema applied after the import")
	endpoint := flag.String("hub", release.Env("HF_ENDPOINT", hfhub.DefaultEndpoint), "the Hugging Face endpoint")
	flag.Parse()

	web, err := releasedb.ReadWebSQL(*webSQL)
	cli.Exit(err)
	var r *release.Release
	file := *dump
	if *manifest != "" {
		r, err = release.Load(*manifest)
		cli.Exit(err)
		if file == "" {
			file = filepath.Join(filepath.Dir(*manifest), r.Dump.Path)
		}
	} else {
		// Downloads are anonymous: the dataset is public.
		hub := hfhub.New(*endpoint, "")
		var path string
		r, path, err = release.Fetch(ctx, hub, *repo, *revision, *manifestPath)
		cli.Exit(err)
		file, err = release.Download(ctx, hub, *repo, r, path, *cache)
		cli.Exit(err)
	}
	result, err := releasedb.Restore(ctx, r, file, releasedb.RestoreOptions{DSN: *dsn, Reader: *reader, PGRestore: *pgRestore, WebSQL: web})
	cli.Exit(err)
	fmt.Println(result)
}
