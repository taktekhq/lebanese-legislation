// Command journal-sitemap prints the sitemap of the project journal (the
// /blog/ pages, in Arabic and English) from the posts built into the
// website. It needs no database.
package main

import (
	"io"
	"log"
	"os"
	"strings"

	"github.com/taktekhq/lebanese-legislation/internal/site"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) != 1 {
		log.Fatal("usage: journal-sitemap > sitemap.xml")
	}
	if err := writeJournalSitemap(os.Stdout); err != nil {
		log.Fatal(err)
	}
}

// writeJournalSitemap exports only the embedded public posts, without a database.
func writeJournalSitemap(out io.Writer) error {
	body, err := site.JournalSitemapXML()
	if err != nil {
		return err
	}
	_, err = io.Copy(out, strings.NewReader(body))
	return err
}
