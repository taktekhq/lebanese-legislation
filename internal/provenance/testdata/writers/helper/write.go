// Package helper is a fixture: a writer file that reads no model output
// itself and calls a helper of its own package that does. ScanWriters must
// flag the package.
package helper

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func Store(ctx context.Context, conn *pgx.Conn) error {
	vs, err := verdicts()
	if err != nil {
		return err
	}
	for k, text := range vs {
		if _, err := conn.Exec(ctx, `INSERT INTO article_version (law_id, article_number, major, minor, text_ar)
			VALUES (1, $1, 2, 0, $2)`, k, text); err != nil {
			return err
		}
	}
	return nil
}
