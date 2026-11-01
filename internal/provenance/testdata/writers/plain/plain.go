// Package plain is a fixture: it reads law text and writes none.
package plain

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func Count(ctx context.Context, conn *pgx.Conn) (n int, err error) {
	err = conn.QueryRow(ctx, `SELECT count(*) FROM legislation_article WHERE text_ar <> ''`).Scan(&n)
	return n, err
}
