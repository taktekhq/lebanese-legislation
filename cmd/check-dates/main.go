// check-dates runs the nightly date check (internal/checkdates).
//
//	check-dates [-db dsn] [-tsv file]
//
// Exit 0 when all hold, 1 on a violation (each listed with its law id), 2 when
// the data cannot be read. It always prints the uncertain dates.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/checkdates"
)

func main() {
	dsn := flag.String("db", defaultDSN(), "Postgres connection string (default LEBLEG_DSN, else the local lebleg database)")
	tsvPath := flag.String("tsv", "db/date_corrections.tsv", "the corrections file")
	flag.Parse()
	b, err := os.ReadFile(*tsvPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-dates:", err)
		os.Exit(2)
	}
	tsv, viol := checkdates.ParseTSV(string(b))
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-dates:", err)
		os.Exit(2)
	}
	defer conn.Close(ctx)
	texts, rows, err := checkdates.Load(ctx, conn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-dates:", err)
		os.Exit(2)
	}
	loc, err := time.LoadLocation("Asia/Beirut")
	if err != nil {
		loc = time.FixedZone("EET", 2*3600)
	}
	viol = append(viol, checkdates.Check(texts, tsv, rows, time.Now().In(loc))...)
	unc := checkdates.Uncertain(rows)
	fmt.Printf("uncertain dates: %d\n", len(unc))
	for _, id := range unc {
		fmt.Printf("  %d\n", id)
	}
	for _, v := range viol {
		fmt.Println("FAIL", v)
	}
	if len(viol) > 0 {
		os.Exit(1)
	}
	fmt.Println("check-dates: ok")
}

func defaultDSN() string {
	if v := os.Getenv("LEBLEG_DSN"); v != "" {
		return v
	}
	return "host=/tmp dbname=lebleg"
}
