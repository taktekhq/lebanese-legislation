// Package viaimport is a fixture: it reads no model output itself, but
// imports a package that does (judgereader, not a decision package) and
// writes law text. ScanWriters must flag it.
package viaimport

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/provenance/testdata/writers/judgereader"
)

func Store(ctx context.Context, conn *pgx.Conn) error {
	if err := judgereader.Link(ctx, conn); err != nil {
		return err
	}
	_, err := conn.Exec(ctx, `UPDATE legislation_article SET text_ar = $1 WHERE law_id = 1`, "")
	return err
}
