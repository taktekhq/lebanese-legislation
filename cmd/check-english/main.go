// check-english checks that English stays an unofficial aid: every surface
// that prints title_en carries the "unofficial" flag, every text added in the
// last 24 hours has an English title, no article body is mostly Latin
// letters, and every title_en row names its model.
//
//	check-english [-db dsn] [-repo dir] [-allow file]
//
// Exit 0 when all hold, 1 on a violation (listed), 2 when the data cannot be read.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/checkenglish"
)

func main() {
	dsn := flag.String("db", os.Getenv("LEBLEG_DSN"), "Postgres connection string (default LEBLEG_DSN)")
	repo := flag.String("repo", ".", "the repo root, where the templates and export code are")
	allow := flag.String("allow", "db/english_articles_allow.txt", "article ids (law_id/number, one per line) allowed to hold English")
	flag.Parse()
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "check-english: no database (-db or LEBLEG_DSN)")
		os.Exit(2)
	}
	viol, err := checkenglish.Surface(*repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-english:", err)
		os.Exit(2)
	}
	allowed := map[string]bool{}
	if b, err := os.ReadFile(*allow); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			if ln = strings.TrimSpace(ln); ln != "" && !strings.HasPrefix(ln, "#") {
				allowed[ln] = true
			}
		}
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-english:", err)
		os.Exit(2)
	}
	defer conn.Close(ctx)
	dv, err := checkenglish.Data(ctx, conn, allowed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-english:", err)
		os.Exit(2)
	}
	viol = append(viol, dv...)
	for _, v := range viol {
		fmt.Println("FAIL", v)
	}
	if len(viol) > 0 {
		os.Exit(1)
	}
	fmt.Println("check-english: ok")
}
