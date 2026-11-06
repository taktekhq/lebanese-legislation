package semantic

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/internal/vectorize"
)

// BuildText rebuilds the metadata database from canonical text using the same
// corpus, hashes and chunk boundaries as Sync, without an embedding provider.
// It is an offline build operation, never a serving-time migration.
func BuildText(ctx context.Context, corpus, index *pgxpool.Pool, model string) (Stats, error) {
	s := NewStore(index, nil).WithVectorize(&vectorize.Client{})
	s.model = model
	if err := s.EnsureSchema(ctx); err != nil {
		return Stats{}, err
	}
	tx, err := index.Begin(ctx)
	if err != nil {
		return Stats{}, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `TRUNCATE semantic_documents, semantic_chunks`); err != nil {
		return Stats{}, err
	}
	var docs, chunks [][]any
	var stats Stats
	flush := func() error {
		if len(docs) > 0 {
			if _, err := tx.CopyFrom(ctx, pgx.Identifier{"semantic_documents"}, []string{"model", "kind", "source_id", "part", "lang", "hash", "seen_run"}, pgx.CopyFromRows(docs)); err != nil {
				return err
			}
		}
		if len(chunks) > 0 {
			if _, err := tx.CopyFrom(ctx, pgx.Identifier{"semantic_chunks"}, []string{"model", "kind", "source_id", "part", "lang", "title", "text", "hash", "chunk_start", "chunk_end"}, pgx.CopyFromRows(chunks)); err != nil {
				return err
			}
		}
		docs, chunks = nil, nil
		return nil
	}
	err = walkCorpus(ctx, corpus, DocKey{}, 0, func(d Document) error {
		docs = append(docs, []any{model, d.Kind, d.ID, d.Part, d.Lang, Hash(d.Text), "data"})
		stats.Documents++
		for _, c := range Chunks(d) {
			chunks = append(chunks, []any{model, d.Kind, d.ID, d.Part, d.Lang, d.Title, c.Text, c.Hash, c.Start, c.End})
			stats.Chunks++
			if len(chunks) >= 2000 {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if len(docs) >= 2000 {
			return flush()
		}
		return nil
	})
	if err != nil {
		return stats, fmt.Errorf("build semantic text: %w", err)
	}
	if err = flush(); err != nil {
		return stats, err
	}
	return stats, tx.Commit(ctx)
}
