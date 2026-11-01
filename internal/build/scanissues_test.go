package build

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/archive"
)

func TestScanIssueID(t *testing.T) {
	taken := map[int]bool{}
	for _, c := range []struct {
		year int
		num  string
		want int
	}{
		{2026, "36", 2002026036},
		{2025, "1", 2002025001},
		{2025, "999", 2002025999},
	} {
		if got := scanIssueID(c.year, c.num, taken); got != c.want {
			t.Errorf("scanIssueID(%d, %q) = %d, want %d", c.year, c.num, got, c.want)
		}
	}
	// Not a plain integer under 1000: the odd range, stable, and never the plain one.
	a := scanIssueID(2026, "36م", taken)
	b := scanIssueID(2026, "ملحق 36", taken)
	if a < ScanIssueOddBase || b < ScanIssueOddBase || a == b {
		t.Errorf("odd ids %d, %d: want two different ids from %d up", a, b, ScanIssueOddBase)
	}
	if again := scanIssueID(2026, "36م", nil); again != a {
		t.Errorf("odd id not stable: %d then %d", a, again)
	}
	for _, n := range []string{"0036", "1000", "", "3.6", "٣٦"} {
		if id := scanIssueID(2026, n, nil); id < ScanIssueOddBase || id >= 2_147_483_647 {
			t.Errorf("%q: id %d outside the odd range", n, id)
		}
	}
	// Taken ids move up by one.
	if x, y := scanIssueID(2026, "36م", map[int]bool{a: true}), a+1; x != y {
		t.Errorf("collision: got %d, want %d", x, y)
	}
}

// The scan Gazette step on a loaded database (LEBLEG_DSN with the scans in):
// every scanned text with a Gazette number is listed in an issue, scan issues
// have the stable ids, and a second run changes nothing.
func TestScanGazetteStep(t *testing.T) {
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
	var scans int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM legislation_scan`).Scan(&scans); err != nil || scans == 0 {
		t.Skip("database has no scanned texts")
	}
	run := func() {
		t.Helper()
		if _, err := Run(ctx, Options{Archive: archive.Archive{Dir: "archive"}, DSN: dsn, Only: "scan-gazette", Log: func(string, ...any) {}}); err != nil {
			t.Fatal(err)
		}
	}
	count := func() (issues, entries int) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM gazette_issue), (SELECT count(*) FROM gazette_entry)`).Scan(&issues, &entries); err != nil {
			t.Fatal(err)
		}
		return
	}
	run()
	i1, e1 := count()
	run()
	if i2, e2 := count(); i1 != i2 || e1 != e2 {
		t.Errorf("second run changed the tables: %d issues, %d entries, then %d, %d", i1, e1, i2, e2)
	}
	var missing int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM legislation_summary
		WHERE id >= $1 AND gazette_number IS NOT NULL AND gazette_issue_id IS NULL`, ScanIDBase).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing > 0 {
		t.Errorf("%d scanned texts with a Gazette number have no issue", missing)
	}
	var bad int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM gazette_issue
		WHERE id >= $1 AND id < $2 AND id <> $1 + extract(year FROM published_on)::int * 1000 + number::int`,
		ScanIssueBase, ScanIssueOddBase).Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad > 0 {
		t.Errorf("%d scan issues do not follow the id rule", bad)
	}
	// A text linked to a Centre issue shares its number and year.
	var wrong int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM gazette_entry e
		JOIN legislation l ON l.id = e.law_id JOIN gazette_issue g ON g.id = e.issue_id
		WHERE e.law_id >= $1 AND g.id < $1 AND (g.number <> l.gazette_number
		  OR g.published_on <> coalesce(l.gazette_date, g.published_on))`, ScanIDBase).Scan(&wrong); err != nil {
		t.Fatal(err)
	}
	if wrong > 0 {
		t.Errorf("%d scanned texts linked to a Centre issue that does not match", wrong)
	}
}
