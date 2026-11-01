// Command usage-stats adds up one day of the anonymous usage record the
// website keeps when it runs with -events (searches, reads, AI sign-ups),
// and prints it as JSON.
//
//	usage-stats -events /var/lib/lebleg -day 2026-10-31
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/site"
)

func main() {
	log.SetFlags(0)
	dir := flag.String("events", "/var/lib/lebleg", "the folder the web server's -events flag writes")
	day := flag.String("day", time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), "the day to add up (UTC); yesterday by default")
	flag.Parse()
	st, err := site.UsageStats(*dir, *day)
	cli.Exit(err)
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	cli.Exit(enc.Encode(st))
}
