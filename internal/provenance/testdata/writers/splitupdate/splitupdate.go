// Package splitupdate is a fixture: it reads the model's answers and
// UPDATEs a text column whose name is joined on after the table, so no one
// string holds both. ScanWriters must flag it.
package splitupdate

import (
	"context"
	"os"

	"github.com/jackc/pgx/v5"
)

func Store(ctx context.Context, conn *pgx.Conn) error {
	raw, err := os.ReadFile("db/model/citations.jsonl")
	if err != nil {
		return err
	}
	q := "UPDATE article_version SET "
	_, err = conn.Exec(ctx, q+"text_ar = $1 WHERE law_id = 1", string(raw))
	return err
}
