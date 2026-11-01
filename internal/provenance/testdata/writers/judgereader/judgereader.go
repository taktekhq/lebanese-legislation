// Package judgereader is a fixture: it reads the model's answers but writes
// no law text (a graph edge), so ScanWriters leaves it alone.
package judgereader

import (
	"context"
	"os"

	"github.com/jackc/pgx/v5"
)

func Link(ctx context.Context, conn *pgx.Conn) error {
	if _, err := os.ReadFile("db/model/citations.jsonl"); err != nil {
		return err
	}
	_, err := conn.Exec(ctx, `INSERT INTO graph_edge (from_key, to_key, relation, source, status, from_type)
		VALUES ('law/1', 'law/2', 'cites', 'model', 'linked', 'legislation')`)
	return err
}
