// Package cli holds what every command in cmd/ shares: the default
// database, the signal context, exit codes and the wording guard's flags.
package cli

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/taktekhq/lebanese-legislation/internal/provenance"
)

// DefaultDSN is the local database, or LEBLEG_DSN (a copy of the data
// elsewhere, as the tests use).
func DefaultDSN() string {
	if v := os.Getenv("LEBLEG_DSN"); v != "" {
		return v
	}
	return "host=/tmp dbname=lebleg"
}

// Context is cancelled on Ctrl-C and on SIGTERM, so a command stopped by
// docker or systemd finishes the work in hand instead of being cut off.
func Context() (context.Context, context.CancelFunc) {
	log.SetFlags(0)
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Exit ends the run on err, like log.Fatal, except that an error carrying
// its own exit code (an ExitCode() int method, such as the wording guard's
// 4) exits with that code, so a script can tell a block from a crash.
func Exit(err error) {
	if err == nil {
		return
	}
	log.Print(err)
	os.Exit(ExitCode(err))
}

// ExitCode is the code Exit exits with: 0 for no error, the error's own
// code when it has one above 0, else 1.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) && coded.ExitCode() > 0 {
		return coded.ExitCode()
	}
	return 1
}

// GuardFlags adds the wording guard's flags to fs; call the result after
// fs.Parse. The defaults are a checkout's own files, from its root.
func GuardFlags(fs *flag.FlagSet) func() provenance.Guard {
	d := provenance.DefaultPaths(".")
	arch := fs.String("archive", d.Archive, "the archive the law texts were read from (the wording guard's sources)")
	scans := fs.String("scans", d.Bundle, "the lebgazette sader-bundle folder (the scanned texts' sources)")
	manifest := fs.String("manifest", "", "the bundle's manifest (default: <-scans>.manifest.tsv, beside the folder)")
	edits := fs.String("edits", strings.Join(d.Edits, ","), "approved edits: approved_edits.tsv and the person-written correction files, comma-separated")
	known := fs.String("known", d.Known, "texts pinned while they are being fixed (provenance_known.tsv)")
	return func() provenance.Guard {
		g, err := provenance.Open(provenance.Paths{Archive: *arch, Bundle: *scans, Manifest: *manifest, Edits: strings.Split(*edits, ","), Known: *known})
		if err != nil {
			log.Fatal(err)
		}
		return g
	}
}

// IfExists returns dir when it exists, else "" (and says so), so a manual/
// folder that was not filled in is skipped instead of failing the run.
func IfExists(dir string) string {
	if dir == "" {
		return ""
	}
	if _, err := os.Stat(dir); err != nil {
		log.Printf("%s: not there, skipped", dir)
		return ""
	}
	return dir
}
