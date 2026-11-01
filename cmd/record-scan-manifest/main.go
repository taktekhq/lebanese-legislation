// Command record-scan-manifest records the SHA-256 of each new file in a
// Gazette scan bundle folder, and never rewrites one it holds. Run it right
// after copying a new bundle in.
//
//	record-scan-manifest -scans dir [-manifest file] [-accept id,...]
//
// A recorded file whose bytes changed is listed and the command exits 4; the
// wording guard refuses that file until a person checks the new bytes and
// names its bundle in -accept. Exit 1 when the manifest could not be written.
package main

import (
	"flag"
	"log"
	"os"
	"strings"

	"github.com/taktekhq/lebanese-legislation/internal/provenance"
)

func main() {
	log.SetFlags(0)
	scans := flag.String("scans", "", "the lebgazette sader-bundle folder")
	manifest := flag.String("manifest", "", "the manifest to record into (default: <-scans>.manifest.tsv, beside the folder)")
	accept := flag.String("accept", "", "bundles (SaderLex ids, comma-separated) whose changed files a person has checked: re-record them")
	flag.Parse()
	if *scans == "" {
		flag.Usage()
		os.Exit(64)
	}
	path := *manifest
	if path == "" {
		path = provenance.ManifestPath(*scans)
	}
	var ids []string
	if *accept != "" {
		ids = strings.Split(*accept, ",")
	}
	r, err := provenance.RecordManifest(*scans, path, ids)
	if err != nil {
		log.Printf("manifest: %v", err)
		os.Exit(1)
	}
	log.Printf("manifest: %s: %d files added, %d unchanged, %d accepted, %d changed", path, r.Added, r.Kept, r.Accepted, len(r.Changed))
	if len(r.Changed) > 0 {
		for _, c := range r.Changed {
			log.Printf("manifest: changed since recorded, old hash kept: %s", c)
		}
		os.Exit(4)
	}
}
