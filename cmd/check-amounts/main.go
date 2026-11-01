// check-amounts runs the nightly amounts check (internal/checkamounts).
//
//	check-amounts [-db dsn] [-tsv file] [-review file] [-expect file]
//
// Exit 0 when all hold, 1 on a violation (each listed), 2 when the data
// cannot be read. It prints how many articles each row reaches.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/checkamounts"
)

func main() {
	dsn := flag.String("db", defaultDSN(), "Postgres connection string (default LEBLEG_DSN, else the local lebleg database)")
	tsvPath := flag.String("tsv", "db/amounts.tsv", "the amounts file")
	reviewPath := flag.String("review", "db/amounts_review.tsv", "the review list (by_law_id, by_article, note)")
	expectPath := flag.String("expect", "testdata/amounts_expected.tsv", "pinned reach per row (by_law_id, by_article, count, source)")
	flag.Parse()
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, "check-amounts:", err)
		os.Exit(2)
	}
	b, err := os.ReadFile(*tsvPath)
	if err != nil {
		fail(err)
	}
	var d checkamounts.Data
	var bad []string
	d.Rows, bad = checkamounts.ParseRows(string(b))
	if len(bad) > 0 {
		fail(fmt.Errorf("%v", bad))
	}
	// The review list and the pins are optional files.
	review, expect := map[string]string{}, map[string]string{}
	for _, f := range []struct {
		path string
		into map[string]string
	}{{*reviewPath, review}, {*expectPath, expect}} {
		raw, err := os.ReadFile(f.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			fail(err)
		}
		m, bad := checkamounts.ParseKeyed(string(raw))
		if len(bad) > 0 {
			fail(fmt.Errorf("%s: %v", f.path, bad))
		}
		for k, v := range m {
			f.into[k] = v
		}
	}
	d.Review = map[string]bool{}
	for k := range review {
		d.Review[k] = true
	}
	d.Expect = map[string]int{}
	for k, v := range expect {
		n, err := strconv.Atoi(v)
		if err != nil {
			fail(fmt.Errorf("%s: count %q", *expectPath, v))
		}
		d.Expect[k] = n
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dsn)
	if err != nil {
		fail(err)
	}
	defer conn.Close(ctx)
	if err := checkamounts.Load(ctx, conn, &d); err != nil {
		fail(err)
	}
	viol, reach := checkamounts.Check(d)
	for _, r := range reach {
		fmt.Println(r)
	}
	for _, v := range viol {
		fmt.Println("FAIL", v)
	}
	if len(viol) > 0 {
		os.Exit(1)
	}
	fmt.Println("check-amounts: ok")
}

func defaultDSN() string {
	if v := os.Getenv("LEBLEG_DSN"); v != "" {
		return v
	}
	return "host=/tmp dbname=lebleg"
}
