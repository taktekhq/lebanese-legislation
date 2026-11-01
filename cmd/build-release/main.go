// Command build-release builds a public database release: it copies the
// public tables from a database loaded from data/ (data/load.sh), derives
// the canonical documents, reuses the vectors of an earlier release whose
// text is unchanged, embeds what is missing with Gemini (GEMINI_API_KEY,
// only with -embed-missing, behind the caps), validates every chunk, and
// writes corpus.dump with its manifest.json. Exit 2 when chunks are still
// missing vectors: the staging database keeps the work; rerun with -resume.
//
//	build-release -db empty-dsn -source-db data-dsn -revision COMMIT -release-id ID -out DIR
//	              [-reuse-db earlier-release-dsn] [-embed-missing]
package main

import (
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"
	_ "time/tzdata" // the budget day follows a named zone, also in containers without zoneinfo

	"github.com/taktekhq/lebanese-legislation/internal/embedding"
	"github.com/taktekhq/lebanese-legislation/internal/release"
	"github.com/taktekhq/lebanese-legislation/internal/releasedb"
)

func main() {
	ctx, stop := release.Context()
	defer stop()
	dsn := flag.String("db", release.Env("DATABASE_URL", ""), "the empty staging database, PostgreSQL 18.6 with pgvector 0.8.2 (default DATABASE_URL)")
	source := flag.String("source-db", release.Env("SOURCE_DATABASE_URL", ""), "a database loaded from data/ by data/load.sh, read only (default SOURCE_DATABASE_URL)")
	calculated := flag.String("calculated", "data/article_calculated_current.jsonl", "the calculated current amounts; empty for none")
	reuse := flag.String("reuse-db", release.Env("REUSE_DATABASE_URL", ""), "an earlier release database to reuse vectors from, read only (default REUSE_DATABASE_URL)")
	repo := flag.String("repo", release.DefaultRepo, "the source repository recorded in the manifest")
	revision := flag.String("revision", "", "the source commit, 40 hex")
	releaseID := flag.String("release-id", "", "the new release id")
	asOf := flag.String("as-of", time.Now().Format("2006-01-02"), "texts in force on this date (YYYY-MM-DD)")
	out := flag.String("out", "", "the output directory for corpus.dump, manifest.json and coverage.json")
	embed := flag.Bool("embed-missing", false, "embed the chunks no earlier release covers (GEMINI_API_KEY; paid)")
	maxNew := flag.Int64("max-new-embeddings", 10000, "refuse before any request when more chunks are missing")
	workers := flag.Int("embedding-workers", 1, "concurrent embedding requests, 1 to 8")
	batch := flag.Int("embedding-batch-size", 32, "texts per embedding request, 1 to 100")
	maxTokens := flag.Int64("max-input-tokens", 0, "a total input token budget for this build, kept in a ledger (0: none)")
	ledger := flag.String("embedding-budget-ledger", "", "the token budget's ledger (default <out>/embedding-budget.json)")
	dailyDir := flag.String("daily-embedding-budget-dir", "", "a directory of daily ledgers shared by every build (empty: no daily cap)")
	dailyUSD := flag.String("daily-embedding-usd", "1", "the daily cap in dollars")
	price := flag.String("embedding-usd-per-million", "0.15", "the embedding price per million input tokens")
	zone := flag.String("budget-timezone", "Asia/Beirut", "the time zone a budget day follows")
	pgDump := flag.String("pg-dump", release.Env("PG_DUMP_BIN", "pg_dump"), "the pg_dump executable")
	resume := flag.Bool("resume", false, "continue this exact staging build after an incomplete run")
	noExport := flag.Bool("no-export", false, "finalize the staging database without writing a dump")
	license := flag.String("license", "other: public-texts-credit-the-centre; https://lebaneselegislation.com/data/", "the dataset licence")
	flag.Parse()

	day, err := time.Parse("2006-01-02", *asOf)
	if err != nil {
		log.Fatal("-as-of must be YYYY-MM-DD")
	}
	if *out == "" {
		log.Fatal("-out is required")
	}
	if *maxTokens < 0 {
		log.Fatal("-max-input-tokens must be positive")
	}
	if *workers < 1 || *workers > 8 || *batch < 1 || *batch > 100 {
		log.Fatal("-embedding-workers must be 1 to 8 and -embedding-batch-size 1 to 100")
	}
	o := releasedb.BuildOptions{DSN: *dsn, SourceDSN: *source, Calculated: *calculated, ReuseDSN: *reuse, Repo: *repo,
		Revision: *revision, ReleaseID: *releaseID, AsOf: day, OutputDir: *out, NoExport: *noExport, Resume: *resume,
		PGDump: *pgDump, License: *license, Log: os.Stderr,
		Fill: releasedb.FillOptions{Cap: *maxNew, Workers: *workers, BatchSize: *batch}}
	if *embed {
		key := os.Getenv("GEMINI_API_KEY")
		gemini, err := embedding.NewGemini(key, embedding.Config{})
		release.Exit(err)
		var provider embedding.Counter = gemini
		if *dailyDir != "" {
			provider, err = embedding.NewDaily(provider, *dailyDir, *dailyUSD, *price, *zone)
			release.Exit(err)
		}
		if *maxTokens > 0 {
			path := *ledger
			if path == "" {
				path = filepath.Join(*out, "embedding-budget.json")
			}
			provider, err = embedding.NewBudgeted(provider, *maxTokens, path)
			release.Exit(err)
		}
		o.Embedder = provider
	}
	err = releasedb.Build(ctx, o)
	if errors.Is(err, releasedb.ErrIncomplete) {
		log.Print(err)
		os.Exit(2)
	}
	release.Exit(err)
}
