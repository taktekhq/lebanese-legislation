// check-citations runs the nightly citation check (internal/citecheck).
// Exit 0 when it passes, 1 on a violation (listed), 2 when its files or the
// database cannot be read. Unresolved citations are listed as data gaps and
// never fail it.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/citecheck"
)

func main() {
	log.SetFlags(0)
	fs := flag.NewFlagSet("check-citations", flag.ExitOnError)
	dsn := fs.String("db", envOr("LEBLEG_DSN", "host=/tmp dbname=lebleg"), "Postgres connection string")
	newIDs := fs.String("new", "", "file of the law ids added in the last 24 hours, one a line (or use -prev)")
	prev := fs.String("prev", "", "file of last night's law ids: the texts not in it are new; rewritten after a pass")
	cites := fs.String("citations", "db/model/citations.jsonl", "the model's citations")
	judges := fs.String("judgements", "db/model/judgements.jsonl", "the model's judgements")
	fs.Parse(os.Args[1:])
	if (*newIDs == "") == (*prev == "") {
		fmt.Fprintln(os.Stderr, "usage: check-citations (-new ids.txt | -prev last-ids.txt) [-db dsn] [-citations f] [-judgements f] [logfile...]  (log files: the new part of the judge and cite logs)")
		os.Exit(2)
	}
	in, err := citecheck.Load(context.Background(), citecheck.Options{DSN: *dsn, NewIDs: *newIDs, PrevIDs: *prev, Citations: *cites,
		Judgements: *judges, Logs: fs.Args(), Now: time.Now().UTC()})
	if err != nil {
		log.Printf("check-citations: cannot read: %v", err)
		os.Exit(2)
	}
	r := citecheck.Check(in)
	for _, g := range r.Gaps {
		fmt.Println("data gap:", g)
	}
	for _, v := range r.Violations {
		fmt.Println("FAIL:", v)
	}
	if !r.OK() {
		os.Exit(1)
	}
	if *prev != "" {
		if err := citecheck.SaveIDs(*prev, in.Current); err != nil {
			log.Printf("check-citations: cannot record %s: %v", *prev, err)
			os.Exit(2)
		}
	}
	fmt.Printf("check-citations: ok (%d amending articles of new texts, %d data gaps)\n", len(in.New), len(r.Gaps))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
