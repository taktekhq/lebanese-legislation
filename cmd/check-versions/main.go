// check-versions runs the nightly article-version check (internal/checkversions).
// Exit 0 all hold, 1 a violation (listed), 2 the data cannot be read.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/checkversions"
)

const query = `
SELECT a.law_id, a.number, a.text_ar,
       f.law_id IS NOT NULL, coalesce(f.state, ''), coalesce(f.text_ar, ''), coalesce(f.pending, false),
       coalesce(v.valid_from::text, ''), coalesce(m.effective_on::text, '')
FROM legislation_article a
LEFT JOIN article_in_force f ON f.law_id = a.law_id AND f.position = a.position
LEFT JOIN LATERAL (
    SELECT valid_from FROM article_version
    WHERE law_id = a.law_id AND article_number = a.number
    ORDER BY major DESC, minor DESC LIMIT 1
) v ON true
LEFT JOIN LATERAL (
    SELECT max(effective_on) AS effective_on FROM amendment
    WHERE law_id = a.law_id AND article_number = a.number
      AND status = 'applied' AND effective_on IS NOT NULL
) m ON true
ORDER BY a.law_id, a.position;`

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "check-versions: "+format+"\n", a...)
	os.Exit(2)
}

func defaultDatabaseDSN() string {
	if dsn := os.Getenv("LEBLEG_DSN"); dsn != "" {
		return dsn
	}
	return "host=/tmp dbname=lebleg"
}

func readPendingBaseline(path string, checkOnly bool) (int, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// The live run starts the baseline on its first pass. Until then a
		// check-only run still checks every article; only the rise is unchecked.
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	prev, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || prev < 0 {
		return 0, false, fmt.Errorf("invalid pending baseline %s", path)
	}
	return prev, false, nil
}

func keepPendingBaseline(path string, pending int, checkOnly bool) error {
	if checkOnly {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pending)+"\n"), 0o644)
}

func main() {
	dsn := flag.String("db", defaultDatabaseDSN(), "Postgres connection string (default LEBLEG_DSN or local database)")
	fixturePath := flag.String("fixture", "testdata/checkversions/art73_law321_2023.txt", "the stored Law 321/2023 wording of Article 73")
	pendingFile := flag.String("pending-file", "scraper/data/logs/pipeline/versions-pending.txt", "last night's pending count")
	checkOnly := flag.Bool("check-only", false, "check without updating the pending baseline")
	flag.Parse()
	f, err := os.Open(*fixturePath)
	if err != nil {
		fail("%v", err)
	}
	phrases, err := checkversions.ReadFixture(f)
	_ = f.Close()
	if err != nil {
		fail("%s: %v", *fixturePath, err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		fail("%v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		fail("connect to database: %v", err)
	}
	rows, err := pool.Query(ctx, query)
	if err != nil {
		fail("%v", err)
	}
	var arts []checkversions.Article
	for rows.Next() {
		var a checkversions.Article
		if err := rows.Scan(&a.LawID, &a.Number, &a.CentreText, &a.InView, &a.State, &a.Text, &a.Pending, &a.LatestVersionDate, &a.LatestAmendmentDate); err != nil {
			fail("%v", err)
		}
		arts = append(arts, a)
	}
	if err := rows.Err(); err != nil {
		fail("%v", err)
	}
	if len(arts) == 0 {
		fail("legislation_article is empty")
	}
	res := checkversions.Check(arts, phrases)
	prev, baselineMissing, err := readPendingBaseline(*pendingFile, *checkOnly)
	if err != nil {
		fail("%v", err)
	}
	if !baselineMissing && checkversions.PendingRose(res.Pending, prev) {
		res.Violations = append(res.Violations, fmt.Sprintf("pending rose from %d to %d (more than 1%%)", prev, res.Pending))
	}
	if baselineMissing && *checkOnly {
		fmt.Println("versions: no prior pending baseline; the rise in pending is not checked")
	} else if baselineMissing {
		fmt.Println("versions: no prior pending baseline; initializing on first passing run")
	}
	fmt.Printf("versions: %d articles, %d ahead of the Centre's text, %d pending\n", res.Total, res.Ahead, res.Pending)
	if len(res.Violations) > 0 {
		for _, v := range res.Violations {
			fmt.Println("violation:", v)
		}
		os.Exit(1)
	}
	if err := keepPendingBaseline(*pendingFile, res.Pending, *checkOnly); err != nil {
		fail("could not keep the pending count: %v", err)
	}
	fmt.Println("versions: ok")
}
