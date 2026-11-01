// export-vectors streams semantic_chunks to portable NDJSON.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/vectorize"
)

func main() { ctx, cancel := cli.Context(); defer cancel(); cli.Exit(run(ctx)) }

func run(ctx context.Context) error {
	dsnDefault := os.Getenv("LEBLEG_VECTOR_DSN")
	if dsnDefault == "" {
		dsnDefault = cli.DefaultDSN()
	}
	dsn := flag.String("dsn", dsnDefault, "PostgreSQL DSN (LEBLEG_VECTOR_DSN, then the CLI default)")
	out := flag.String("out", "vectors.ndjson", "output NDJSON file")
	flag.Parse()
	conn, err := pgx.Connect(ctx, *dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `DECLARE vector_export NO SCROLL CURSOR FOR SELECT model,kind,source_id,part,lang,hash,chunk_start,embedding::vector::real[] FROM public.semantic_chunks`); err != nil {
		return err
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()
	writer := bufio.NewWriterSize(f, 1<<20)
	enc := json.NewEncoder(writer)
	for {
		rows, err := tx.Query(ctx, `FETCH FORWARD 1000 FROM vector_export`)
		if err != nil {
			return err
		}
		n := 0
		for rows.Next() {
			var model, kind, part, lang, hash string
			var sourceID, start int
			var values []float32
			if err = rows.Scan(&model, &kind, &sourceID, &part, &lang, &hash, &start, &values); err != nil {
				rows.Close()
				return err
			}
			if len(values) != 768 {
				rows.Close()
				return fmt.Errorf("chunk %s:%d has %d dimensions, expected 768", kind, sourceID, len(values))
			}
			row := struct {
				ID       string    `json:"id"`
				Document string    `json:"document"`
				Model    string    `json:"model"`
				Hash     string    `json:"hash"`
				Values   []float32 `json:"values"`
			}{vectorize.ChunkID(model, kind, sourceID, part, lang, hash, start), fmt.Sprintf("%s:%d", kind, sourceID), model, hash, values}
			if err = enc.Encode(row); err != nil {
				rows.Close()
				return err
			}
			n++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if err = writer.Flush(); err != nil {
		return err
	}
	return f.Close()
}
