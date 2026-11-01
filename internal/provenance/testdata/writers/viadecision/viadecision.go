// Package viadecision is a fixture: it writes law text and imports the
// decisions fixture. Declared a decision package, decisions is let through.
package viadecision

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/provenance/testdata/writers/decisions"
)

func Store(ctx context.Context, conn *pgx.Conn) error {
	if st, ok := decisions.Verdict("1/1"); !ok || st == "" {
		return nil
	}
	_, err := conn.Exec(ctx, `INSERT INTO legislation_article (law_id, position, number, text_ar) VALUES (1, 1, '1', '')`)
	return err
}
