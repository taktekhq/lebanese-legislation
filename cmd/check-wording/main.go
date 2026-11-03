// Command check-wording is the wording guard: it checks that every law text
// in the database is a span of its archived source (internal/provenance).
// export-data and the Hugging Face release run the same check
// before they write anything.
//
//	check-wording [-db dsn] [-report] [-archive dir] [-scans dir] [-manifest file] [-edits files] [-known file]
//
// Exit 0 when every text is in its source, 4 when any is not (each listed
// with -report), 1 when the check could not run.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/provenance"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	dsn := flag.String("db", cli.DefaultDSN(), "Postgres connection string")
	report := flag.Bool("report", false, "list every text without its source, with its hash (for provenance_known.tsv)")
	guard := cli.GuardFlags(flag.CommandLine)
	flag.Parse()
	g := guard()
	vs, st, err := provenance.CheckDBStats(ctx, *dsn, g.Sources, g.Edits, g.Known)
	if err != nil {
		log.Printf("wording guard: %v", err)
		os.Exit(1)
	}
	if st.Inferred {
		log.Printf("wording guard: the database predates the provenance columns; sources inferred from ids")
	}
	if *report {
		for _, v := range vs {
			ver := "article"
			switch {
			case v.Row != "":
				ver = v.Row
			case v.VersionID > 0:
				ver = fmt.Sprintf("version %d.%d", v.VersionID/1000, v.VersionID%1000)
			}
			fmt.Printf("law %d\tarticle %s\t%s\t%s\tsha256 %s\n", v.LawID, v.Article, ver, v.Reason, v.SHA256)
		}
	}
	if len(vs) > 0 {
		log.Print((&provenance.BlockedError{Violations: vs}).Error())
		os.Exit(4)
	}
	log.Printf("wording guard: %d texts (%d of them amendments' wordings) from %d sources, all in their sources", st.Texts, st.Amendments, st.Sources)
}
