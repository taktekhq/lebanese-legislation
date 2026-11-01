// Package copyhelper is a fixture: it reads the model's answers and writes
// them as law text through a helper of its own that takes the table as a
// variable, so no write pattern sees the table at the CopyFrom. ScanWriters
// must flag the call that names the table.
package copyhelper

import (
	"context"
	"os"

	"github.com/jackc/pgx/v5"
)

func copyRows(ctx context.Context, conn *pgx.Conn, table string, cols []string, rows [][]any) error {
	_, err := conn.CopyFrom(ctx, pgx.Identifier{table}, cols, pgx.CopyFromRows(rows))
	return err
}

func Store(ctx context.Context, conn *pgx.Conn) error {
	raw, err := os.ReadFile("db/model/judgements.jsonl")
	if err != nil {
		return err
	}
	return copyRows(ctx, conn, "legislation_article", []string{"law_id", "number", "text_ar"}, [][]any{{1, "1", string(raw)}})
}
