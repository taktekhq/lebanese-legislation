package build

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/archive"
)

// The indexing step on a loaded database (LEBLEG_DSN, as `lebleg build` left
// it): it is repeatable, a page whose content has not changed keeps its
// changed_at, and the placeholder documents are listed.
func TestIndexingStepOnLoadedDatabase(t *testing.T) {
	dsn := os.Getenv("LEBLEG_DSN")
	if dsn == "" {
		t.Skip("no LEBLEG_DSN: a database built by lebleg build")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("no database: %v", err)
	}
	defer conn.Close(ctx)
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.page_state') IS NOT NULL AND to_regclass('public.noindex_page') IS NOT NULL`).Scan(&ok); err != nil || !ok {
		t.Skip("database has no page_state or noindex_page")
	}
	run := func() {
		t.Helper()
		if _, err := Run(ctx, Options{Archive: archive.Archive{Dir: "archive"}, DSN: dsn, Only: "indexing", Log: func(string, ...any) {}}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() (map[string]time.Time, int) {
		t.Helper()
		rows, err := conn.Query(ctx, `SELECT kind || ':' || id, changed_at FROM page_state`)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]time.Time{}
		for rows.Next() {
			var k string
			var at time.Time
			if err := rows.Scan(&k, &at); err != nil {
				t.Fatal(err)
			}
			m[k] = at
		}
		var n int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM noindex_page`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return m, n
	}
	run()
	first, thin := snapshot()
	run()
	second, thin2 := snapshot()
	if len(first) == 0 || len(first) != len(second) || thin != thin2 || thin == 0 {
		t.Fatalf("page_state %d then %d rows, noindex_page %d then %d", len(first), len(second), thin, thin2)
	}
	moved := 0
	for k, at := range first {
		if !second[k].Equal(at) {
			moved++
		}
	}
	if moved != 0 {
		t.Errorf("%d pages changed date between two runs on the same data", moved)
	}
	var future, texts, laws int
	conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE changed_at > now()), count(*) FILTER (WHERE kind = 'law') FROM page_state`).Scan(&future, &texts)
	conn.QueryRow(ctx, `SELECT count(*) FROM legislation`).Scan(&laws)
	if future != 0 || texts != laws {
		t.Errorf("%d pages dated in the future; %d law states for %d texts", future, texts, laws)
	}
	// a page that is changed gets today's date: damage one hash and rerun
	if _, err := conn.Exec(ctx, `UPDATE page_state SET content_hash = 'x' WHERE kind = 'law' AND id = (SELECT min(id) FROM legislation)`); err != nil {
		t.Fatal(err)
	}
	run()
	var id int
	conn.QueryRow(ctx, `SELECT min(id) FROM legislation`).Scan(&id)
	third, _ := snapshot()
	k := "law:" + itoa(id)
	if time.Since(third[k]) > time.Hour {
		t.Errorf("a page whose content changed is dated %v", third[k])
	}
	var placeholders int
	conn.QueryRow(ctx, `SELECT count(*) FROM noindex_page WHERE kind = 'agreement' AND reason = 'placeholder'`).Scan(&placeholders)
	var titled int
	conn.QueryRow(ctx, `SELECT count(*) FROM agreement WHERE title ~ 'غير متوفر'`).Scan(&titled)
	if placeholders != titled {
		t.Errorf("%d placeholder agreements listed, %d have the placeholder title", placeholders, titled)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
