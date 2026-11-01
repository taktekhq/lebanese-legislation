// Command ul-index lists the texts the Centre published from a given year, from archive/.
//
//	ul-index [-archive dir] [-from year]
package main

import (
	"flag"
	"log"
	"os"

	"github.com/taktekhq/lebanese-legislation/internal/archive"
	"github.com/taktekhq/lebanese-legislation/internal/official"
)

func main() {
	log.SetFlags(0)
	dir := flag.String("archive", "archive", "the git-tracked archive directory")
	from := flag.Int("from", 2025, "texts issued in or after this year")
	flag.Parse()
	if err := official.ULIndex(archive.Archive{Dir: *dir}, *from, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
