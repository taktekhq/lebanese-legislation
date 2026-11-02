package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/db"
)

func TestUpdatedAtSchema(t *testing.T) {
	for _, table := range []string{"legislation", "ruling", "gazette_issue"} {
		start := strings.Index(Schema(), "CREATE TABLE "+table+" (")
		body, _, _ := strings.Cut(Schema()[start:], ");")
		if !strings.Contains(body, "updated_at") {
			t.Errorf("%s has no updated_at column", table)
		}
	}
	if changedOn(nil) != nil || changedOn(ptrTime(time.Date(1964, 3, 1, 0, 0, 0, 0, time.UTC))) != nil {
		t.Error("an unknown date or the text's own date became updated_at")
	}
	if d := changedOn(ptrTime(time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC))); d == nil || *d != "2026-10-30" {
		t.Errorf("changedOn = %v", d)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// The snapshot carries each text's, ruling's and Gazette issue's updated_at
// from page_state, over the MCP server's fixture (internal/mcpserver/testdata).
func TestExportUpdatedAt(t *testing.T) {
	dsn := os.Getenv("LEBLEG_TEST_DSN")
	if dsn == "" {
		t.Skip("set LEBLEG_TEST_DSN to a disposable Postgres database")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("snapshot_test_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture, err := os.ReadFile("../mcpserver/testdata/fixture.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{db.Schema, db.AmendmentSchema, db.GraphSchema, string(fixture)} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	ch, err := newChunker(dir, maxFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	ov, err := newOversizeWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []func(context.Context, *pgxpool.Pool, *chunker, *oversizeWriter) (int, error){exportLegislation, exportRulings, exportGazetteIssues} {
		if _, err := fn(ctx, pool, ch, ov); err != nil {
			t.Fatal(err)
		}
	}
	if err := ch.close(); err != nil {
		t.Fatal(err)
	}
	ov.close()
	var sql strings.Builder
	for _, f := range ch.files {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		sql.Write(b)
	}
	out := sql.String()
	for _, want := range []string{
		"source_url,title_en,updated_at)", "source_url,pdf_url,updated_at)", "published_on,source_url,updated_at)",
		"(500,'123','2001-01-05',NULL,NULL)", // issue 500's page_state date is the issue's own: none
	} {
		if !strings.Contains(out, want) {
			t.Errorf("snapshot SQL missing %q", want)
		}
	}
	for _, start := range []string{"(100,'قانون',", "(9000,'محكمة التمييز',"} {
		i := strings.Index(out, start)
		if i < 0 {
			t.Fatalf("no row %s", start)
		}
		row := out[i:]
		for _, end := range []string{"),(", ");"} {
			row, _, _ = strings.Cut(row, end)
		}
		if !strings.HasSuffix(row, ",'2026-10-30'") {
			t.Errorf("row %s does not end with updated_at 2026-10-30: %s", start, row)
		}
	}
}
