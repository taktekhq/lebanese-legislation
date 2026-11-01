// Package modelout is the one place the build reads model output: the
// judge's answers (db/model/judgements.jsonl), the citer's links
// (db/model/*citations*.jsonl), the machine readings of scanned judgments
// (*.read.json) and the English titles (db/titles_en.tsv).
//
// It writes only tables that hold no law text (graph_edge, ruling_page,
// ruling_file, legislation_title_en), and what it hands back to the build
// is decisions and counts, never a text: a Verdict says which version of an
// article stands, never what one says. internal/build, whose writers store
// law text, reads model output only through this package. The wording
// guard's static check (internal/provenance ScanWriters) holds both to that:
// a package that writes law text may not read model output, nor import a
// package that does, except this one, whose exported results carry no free
// text.
package modelout

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// lawText are the tables that hold law text, which this package never
// writes (the wording guard's static check holds the code to that too).
var lawText = map[string]bool{"legislation": true, "legislation_article": true, "article_version": true, "amendment": true}

// copyRows writes rows into table; no rows is no write. A law-text table is
// refused before anything is written.
func copyRows(ctx context.Context, conn *pgx.Conn, table string, cols []string, rows [][]any) error {
	if lawText[table] {
		return fmt.Errorf("copy %s: modelout writes no law text", table)
	}
	if len(rows) == 0 {
		return nil
	}
	if _, err := conn.CopyFrom(ctx, pgx.Identifier{table}, cols, pgx.CopyFromRows(rows)); err != nil {
		return fmt.Errorf("copy %s: %w", table, err)
	}
	return nil
}
