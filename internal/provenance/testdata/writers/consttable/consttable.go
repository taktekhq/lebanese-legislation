// Package consttable is a fixture: it reads the model's answers and copies
// them into a text table it names in a constant. ScanWriters must flag it.
package consttable

import (
	"context"
	"os"

	"github.com/jackc/pgx/v5"
)

const table = "amendment"

func Store(ctx context.Context, conn *pgx.Conn) error {
	raw, err := os.ReadFile("db/model/judgements.jsonl")
	if err != nil {
		return err
	}
	_, err = conn.CopyFrom(ctx, pgx.Identifier{table}, []string{"text_ar"}, pgx.CopyFromRows([][]any{{string(raw)}}))
	return err
}
