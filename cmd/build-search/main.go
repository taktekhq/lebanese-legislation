// build-search builds semantic text and chunk metadata without embedding calls.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/semantic"
	"github.com/taktekhq/lebanese-legislation/internal/vectorize"
)

func main() {
	ctx, cancel := cli.Context()
	defer cancel()
	corpusDSN := flag.String("corpus-dsn", os.Getenv("DATABASE_URL"), "canonical database DSN")
	indexDSN := flag.String("index-dsn", os.Getenv("SEARCH_DATABASE_URL"), "empty metadata database DSN")
	model := flag.String("model", semantic.DefaultModel, "embedding model whose IDs to generate")
	ids := flag.String("ids-out", "", "optional current id,hash CSV output")
	idSQL := flag.Bool("id-sql", false, "print the SQL expression for a chunk's id (as in vectors.parquet) over semantic_chunks columns, and exit")
	flag.Parse()
	if *idSQL {
		fmt.Println(vectorize.ChunkIDSQL)
		return
	}
	corpus, err := pgxpool.New(ctx, *corpusDSN)
	if err != nil {
		cli.Exit(err)
		return
	}
	defer corpus.Close()
	index, err := pgxpool.New(ctx, *indexDSN)
	if err != nil {
		cli.Exit(err)
		return
	}
	defer index.Close()
	stats, err := semantic.BuildText(ctx, corpus, index, *model)
	if err != nil {
		cli.Exit(err)
		return
	}
	fmt.Printf("documents=%d chunks=%d\n", stats.Documents, stats.Chunks)
	if *ids != "" {
		f, err := os.Create(*ids)
		if err != nil {
			cli.Exit(err)
			return
		}
		defer f.Close()
		conn, err := index.Acquire(ctx)
		if err != nil {
			cli.Exit(err)
			return
		}
		defer conn.Release()
		_, err = conn.Conn().PgConn().CopyTo(ctx, f, `COPY (SELECT `+vectorize.ChunkIDSQL+` AS id,hash FROM semantic_chunks ORDER BY 1) TO STDOUT WITH (FORMAT CSV, HEADER)`)
		if err == nil {
			err = f.Close()
		}
		cli.Exit(err)
	}
}
