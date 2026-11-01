package build

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestReportCountsUniqueRulingArticleLinks builds its two tables in public (the report counts public tables only) in a
// transaction it rolls back, so it needs a disposable database: deploy/checks-dbtest.sh runs it on one.
func TestReportCountsUniqueRulingArticleLinks(t *testing.T) {
	dsn := os.Getenv("LEBLEG_TEST_DSN")
	if dsn == "" {
		t.Skip("set LEBLEG_TEST_DSN to a disposable Postgres database")
	}
	ctx := context.Background()
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close(ctx) }()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	conn := tx.Conn()
	if _, err := conn.Exec(ctx, `CREATE TABLE graph_edge(from_type text, status text, from_id int, to_law_id int, to_article text, from_article text);
		CREATE TABLE legislation_article(law_id int, number text, position int);
		INSERT INTO legislation_article VALUES (1, '28', 1);
		INSERT INTO graph_edge VALUES ('ruling','linked',7,1,'28',NULL), ('ruling','linked',7,1,'28',NULL), ('ruling','linked',8,1,'28',NULL), ('ruling','no_article',9,1,'28',NULL);`); err != nil {
		t.Fatal(err)
	}
	r := Report{}
	if err := r.count(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if r.RulingArticleLinks != 2 {
		t.Fatalf("RulingArticleLinks = %d, want 2", r.RulingArticleLinks)
	}
	var out strings.Builder
	r.Write(&out)
	if !strings.Contains(out.String(), "ruling-to-article links rendered 2") {
		t.Errorf("report missing count: %s", out.String())
	}
}
