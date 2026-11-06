// searchindex maintains the independent pgvector corpus search database.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/semantic"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	cli.Exit(run(ctx, os.Args[1:], os.Stdout))
}

func run(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("searchindex", flag.ContinueOnError)
	fs.SetOutput(out)
	corpusDSN := fs.String("corpus-dsn", "", "Canonical corpus PostgreSQL DSN (or DATABASE_URL)")
	indexDSN := fs.String("index-dsn", "", "Independent index PostgreSQL DSN (or SEARCH_DATABASE_URL)")
	model := fs.String("model", semantic.DefaultModel, "Embedding model; changing model requires reindexing")
	bulk := fs.Bool("bulk", false, "Defer creating an absent ANN index until successful sync; never drops an existing index")
	progress := fs.Int("progress", 10000, "Print progress after this many completed chunks; zero disables")
	dryRun := fs.Bool("dry-run", false, "Count eligible documents and chunks without contacting Gemini or the index database")
	schemaOnly := fs.Bool("schema-only", false, "Create pgvector extension, table and indexes, then exit")
	opts := semantic.SyncOptions{}
	fs.IntVar(&opts.Limit, "limit", 0, "Maximum canonical documents; zero indexes all")
	after := fs.String("after", "", "Resume after this document, kind/id/part/lang in corpus order (there is no -offset: a count shifts when the corpus changes)")
	fs.IntVar(&opts.BatchSize, "batch", 100, "Embedding texts per request (1..100)")
	fs.IntVar(&opts.GroupSize, "group", 100, "Documents per worker group (1..1000); larger groups fill API batches more efficiently")
	fs.IntVar(&opts.Workers, "workers", 1, "Concurrent document workers (1..32)")
	fs.DurationVar(&opts.Rate, "rate", 0, "Minimum interval between batch starts across workers, for example 1s")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *after != "" {
		k, err := semantic.ParseDocKey(*after)
		if err != nil {
			return err
		}
		opts.After = k
	}
	if *corpusDSN == "" {
		*corpusDSN = os.Getenv("DATABASE_URL")
	}
	if *indexDSN == "" {
		*indexDSN = os.Getenv("SEARCH_DATABASE_URL")
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *progress < 0 || opts.GroupSize < 1 || opts.GroupSize > 1000 || opts.Limit < 0 || opts.BatchSize < 1 || opts.BatchSize > 100 || opts.Workers < 1 || opts.Workers > 32 || opts.Rate < 0 {
		return errors.New("invalid index bounds, batch size, group size, workers or rate")
	}
	if !*dryRun && *indexDSN == "" {
		return errors.New("index DSN is required")
	}
	if (!*schemaOnly || *dryRun) && *corpusDSN == "" {
		return errors.New("corpus DSN is required")
	}
	if *dryRun {
		corpus, err := pgxpool.New(ctx, *corpusDSN)
		if err != nil {
			return errors.New("invalid corpus database configuration")
		}
		defer corpus.Close()
		stats, err := semantic.Count(ctx, corpus, opts)
		if err != nil {
			return errors.New("could not count canonical corpus")
		}
		_, err = fmt.Fprintf(out, "documents=%d chunks=%d minimum_embedding_batches=%d\n", stats.Documents, stats.Chunks, (stats.Chunks+opts.BatchSize-1)/opts.BatchSize)
		return err
	}

	var embedder semantic.Embedder
	if !*schemaOnly {
		key := os.Getenv("GEMINI_API_KEY")
		if key == "" {
			key = os.Getenv("GOOGLE_API_KEY")
		}
		if key == "" {
			return errors.New("GEMINI_API_KEY or GOOGLE_API_KEY is required in the environment")
		}
		var err error
		embedder, err = semantic.NewGemini(key, *model)
		if err != nil {
			return err
		}
	}
	// Connection failures deliberately omit DSNs and raw driver errors, which can
	// contain account details supplied in connection strings.
	index, err := pgxpool.New(ctx, *indexDSN)
	if err != nil {
		return errors.New("invalid index database configuration")
	}
	defer index.Close()
	store := semantic.NewStore(index, embedder)
	if *bulk {
		err = store.EnsureStorage(ctx)
	} else {
		err = store.EnsureSchema(ctx)
	}
	if err != nil {
		var storage *semantic.StorageError
		if errors.As(err, &storage) {
			return storage
		}
		return errors.New("could not initialize index database schema")
	}
	if *schemaOnly {
		_, err = fmt.Fprintln(out, "Index schema ready.")
		return err
	}
	corpus, err := pgxpool.New(ctx, *corpusDSN)
	if err != nil {
		return errors.New("invalid corpus database configuration")
	}
	defer corpus.Close()
	if *progress > 0 {
		last := 0
		opts.Progress = func(stats semantic.Stats) {
			done := stats.Embedded + stats.Skipped
			if done-last >= *progress {
				_, _ = fmt.Fprintf(out, "progress documents=%d chunks=%d embedded=%d skipped=%d\n", stats.Documents, stats.Chunks, stats.Embedded, stats.Skipped)
				last = done
			}
		}
	}
	stats, err := store.Sync(ctx, corpus, opts)
	_, writeErr := fmt.Fprintf(out, "documents=%d chunks=%d embedded=%d skipped=%d pruned=%d\n", stats.Documents, stats.Chunks, stats.Embedded, stats.Skipped, stats.Pruned)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var service *semantic.ServiceError
		if errors.As(err, &service) {
			return fmt.Errorf("index sync paused: embedding HTTP %d; completed batches are saved, rerun to resume", service.Status)
		}
		return errors.New("index sync failed; completed batches are saved, rerun to resume")
	}
	if *bulk {
		if err = store.BuildIndex(ctx); err != nil {
			return errors.New("vectors saved but ANN index construction failed; rerun schema-only to retry")
		}
	}
	return writeErr
}
