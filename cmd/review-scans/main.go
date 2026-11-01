// Command review-scans shows, before a release, how each text read from Gazette scans will be cut and loaded.
//
//	review-scans -scans dir
package main

import (
	"flag"
	"log"
	"os"

	"github.com/taktekhq/lebanese-legislation/internal/build"
)

func main() {
	log.SetFlags(0)
	// Read before a release: how each scanned text will be cut and loaded.
	scans := flag.String("scans", "", "a lebgazette sader-bundle folder")
	flag.Parse()
	if *scans == "" {
		log.Fatal("review-scans needs -scans")
	}
	if err := build.ReviewScans(*scans, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
