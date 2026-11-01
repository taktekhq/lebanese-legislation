// Command ruling-read transcribes the downloaded judgments with Gemini through the agy CLI.
//
//	ruling-read [-dir dir] [-workers n] [-watch 10m]
package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/rulingfile"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	dir := flag.String("dir", "ruling-files", "the folder ruling-files wrote")
	agy := flag.String("agy", "agy", "the Antigravity CLI")
	model := flag.String("model", "gemini-3.8-flash-low", "")
	workers := flag.Int("workers", 6, "agy calls at once")
	chunk := flag.Int("chunk", 8, "pages per call")
	dpi := flag.Int("dpi", 200, "page render resolution")
	timeout := flag.Duration("timeout", 15*time.Minute, "per agy call")
	only := flag.String("ids", "", "comma-separated ruling ids (default: every pending one)")
	watch := flag.Duration("watch", 0, "when done, look for newly downloaded PDFs this often (0: exit)")
	flag.Parse()
	r := &rulingfile.Reader{Dir: *dir, Agy: *agy, Model: *model, Workers: *workers, ChunkPages: *chunk,
		DPI: *dpi, Timeout: *timeout, Log: log.Printf}
	for {
		var ids []int
		var err error
		if *only != "" {
			for _, s := range strings.Split(*only, ",") {
				var id int
				fmt.Sscan(s, &id)
				ids = append(ids, id)
			}
		} else if ids, err = r.Pending(); err != nil {
			log.Fatal(err)
		}
		if len(ids) > 0 {
			log.Printf("ruling-read: %d to read", len(ids))
			if err := r.Run(ctx, ids); err != nil {
				log.Fatal(err)
			}
		}
		if *watch == 0 || *only != "" || ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(*watch):
		case <-ctx.Done():
		}
	}
}
