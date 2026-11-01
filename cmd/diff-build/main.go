// Command diff-build builds two git refs side by side and reports how the database changed between them.
//
//	diff-build -old ref -new ref [-expected file] [-report file]
package main

import (
	"flag"
	"os"

	"github.com/taktekhq/lebanese-legislation/internal/build"
	"github.com/taktekhq/lebanese-legislation/internal/cli"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	oldRef := flag.String("old", "", "old git ref")
	newRef := flag.String("new", "", "new git ref")
	expected := flag.String("expected", "", "expected-diff.tsv or none")
	report := flag.String("report", "diff-build-report.txt", "report path")
	repo := flag.String("repo", "", "repository root")
	archiveDir := flag.String("archive", "", "shared archive directory")
	scans := flag.String("scans", "", "shared scans directory")
	rulingFiles := flag.String("ruling-files", "", "shared ruling files directory")
	adminDSN := flag.String("admin-dsn", os.Getenv("LEBLEG_DIFF_ADMIN_DSN"), "maintenance database DSN (never a live corpus DB)")
	flag.Parse()
	cli.Exit(build.RunDiff(ctx, build.DiffOptions{OldRef: *oldRef, NewRef: *newRef, Expected: *expected,
		Report: *report, Repo: *repo, Archive: *archiveDir, Scans: *scans, RulingFiles: *rulingFiles, DSN: *adminDSN}))
}
