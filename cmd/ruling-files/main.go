// Command ruling-files downloads the full judgments (PDF scans) behind ruling pages.
//
//	ruling-files [-archive dir] [-out dir] [-workers n] [-pause-while cmd]
package main

import (
	"flag"
	"log"
	"os/exec"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/archive"
	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/rulingfile"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	dir := flag.String("archive", "archive", "the git-tracked archive directory")
	out := flag.String("out", "ruling-files", "where the PDFs and index.tsv go")
	base := flag.String("base", "https://legallaw.ul.edu.lb", "the Centre's site")
	workers := flag.Int("workers", 3, "parallel requests (the Centre's server collapses past 6 in total)")
	gap := flag.Duration("gap", time.Second, "minimum time between requests, shared by all workers")
	timeout := flag.Duration("timeout", 90*time.Second, "per request")
	ua := flag.String("user-agent", "Mozilla/5.0 (compatible; lebaneselegislation.com)", "")
	pause := flag.String("pause-while", "", "shell command; while it exits 0 (e.g. another crawl is active), wait")
	flag.Parse()
	ids, err := rulingfile.Linked(archive.Archive{Dir: *dir})
	if err != nil {
		log.Fatal(err)
	}
	f := &rulingfile.Fetcher{Base: *base, Dir: *out, Workers: *workers, Gap: *gap, MaxGap: time.Minute,
		Timeout: *timeout, UserAgent: *ua, Log: log.Printf}
	if *pause != "" {
		f.PauseWhile = func() bool { return exec.Command("sh", "-c", *pause).Run() == nil }
	}
	if err := f.Run(ctx, ids); err != nil {
		log.Fatal(err)
	}
}
