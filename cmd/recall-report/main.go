// Command recall-report runs the nightly recall check:
// it compares the articles with an amending verb to the amendment rows built
// from them, and exits 1 when the ratio falls. Exit 2: the database or the
// baseline cannot be read.
package main

import (
	"context"
	"flag"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/internal/recall"
)

type pgSource struct{ dsn string }

func (s pgSource) Load(ctx context.Context) ([]recall.Article, map[recall.Key]bool, error) {
	pool, err := pgxpool.New(ctx, s.dsn)
	if err != nil {
		return nil, nil, err
	}
	defer pool.Close()
	rows, err := pool.Query(ctx, `SELECT law_id, number, text_ar FROM legislation_article`)
	if err != nil {
		return nil, nil, err
	}
	var arts []recall.Article
	for rows.Next() {
		var a recall.Article
		if err := rows.Scan(&a.LawID, &a.Number, &a.Text); err != nil {
			return nil, nil, err
		}
		arts = append(arts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rr, err := pool.Query(ctx, `SELECT DISTINCT by_law_id, by_article FROM amendment`)
	if err != nil {
		return nil, nil, err
	}
	keys := map[recall.Key]bool{}
	for rr.Next() {
		var k recall.Key
		if err := rr.Scan(&k.LawID, &k.Number); err != nil {
			return nil, nil, err
		}
		keys[k] = true
	}
	return arts, keys, rr.Err()
}

func main() {
	dsn := flag.String("db", defaultDSN(), "Postgres connection string (default: LEBLEG_DSN, else the local lebleg)")
	base := flag.String("baseline", "scraper/data/logs/recall-baseline.json", "the last passing night's counts")
	init := flag.Bool("init", false, "write the baseline from tonight's count when there is none")
	flag.Parse()
	if *dsn == "" {
		os.Stderr.WriteString("recall-report: no database: set -db or LEBLEG_DSN\n")
		os.Exit(2)
	}
	os.Exit(recall.Run(context.Background(), pgSource{*dsn}, *base, *init, os.Stdout, os.Stderr))
}

// defaultDSN matches lebleg: LEBLEG_DSN, else the local database.
func defaultDSN() string {
	if v := os.Getenv("LEBLEG_DSN"); v != "" {
		return v
	}
	return "host=/tmp dbname=lebleg"
}
