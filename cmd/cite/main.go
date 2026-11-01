// Command cite asks Gemini to write down every text and article each amending article changes (internal/cite).
//
//	cite [-db dsn] [-sample test|all] [-report] [-preview file]
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/cite"
	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/judge"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	// Ask Gemini to write down, in one fixed form, every text and
	// article each amending article changes (internal/cite). It reads;
	// it never picks our records or writes the law's wording. -report
	// only compares what's saved with the pattern pass, no model.
	dsn := flag.String("db", cli.DefaultDSN(), "Postgres connection string")
	sample := flag.String("sample", "test", `"test" (a fixed 200) or "all" (every amending article)`)
	model := flag.String("model", "gemini-pro-latest", "Gemini model")
	out := flag.String("out", "db/model/citations.jsonl", "where the citations are kept")
	limit := flag.Int("limit", 0, "read at most this many (0: all)")
	workers := flag.Int("workers", 4, "requests in parallel")
	report := flag.Bool("report", false, "compare the saved citations of the sample with the pattern pass; no model")
	preview := flag.String("preview", "", "write an HTML preview of the linked display to this file; no model")
	redo := flag.String("redo", "", `ask again about articles a model whose name contains this answered ("flash")`)
	codes := flag.Bool("codes", false, "only articles whose saved citations name one of the principal codes")
	hard := flag.Bool("hard", false, "only articles a model left unsettled (a quote not in the article, an unmatched change)")
	base := flag.String("base", "https://lebaneselegislation.com", "the site's address, for links in the preview")
	flag.Parse()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	items, err := cite.Items(ctx, pool, *sample)
	if err != nil {
		log.Fatal(err)
	}
	if *report || *preview != "" {
		all, err := cite.Load(*out)
		if err != nil {
			log.Fatal(err)
		}
		cs := map[string]cite.Citation{}
		for _, it := range items {
			if c, ok := all[it.Key]; ok {
				cs[it.Key] = c
			}
		}
		if *preview != "" {
			f, err := os.Create(*preview)
			if err != nil {
				log.Fatal(err)
			}
			if err := cite.Preview(ctx, pool, cs, 10, *base, f); err != nil {
				log.Fatal(err)
			}
			if err := f.Close(); err != nil {
				log.Fatal(err)
			}
			return
		}
		if err := cite.Report(ctx, pool, cs, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *codes || *hard {
		all, err := cite.Load(*out)
		if err != nil {
			log.Fatal(err)
		}
		ix, err := cite.LawIndex(ctx, pool)
		if err != nil {
			log.Fatal(err)
		}
		var keep []cite.Item
		for _, it := range items {
			c, ok := all[it.Key]
			if ok && ((*codes && cite.TouchesCode(ix, it.LawID, c)) || (*hard && cite.Hard(ix, it.LawID, c))) {
				keep = append(keep, it)
			}
		}
		log.Printf("%d of %d articles selected (codes %v, hard %v)", len(keep), len(items), *codes, *hard)
		items = keep
	}
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		log.Fatal("cite: set GEMINI_API_KEY")
	}
	g := &judge.Gemini{Key: key, Model: *model, HTTP: &http.Client{Timeout: 5 * time.Minute}}
	st, err := cite.Run(ctx, g, items, *out, *workers, *limit, *redo, func(f string, a ...any) { log.Printf(f, a...) })
	log.Printf("read %d, skipped %d, failed %d; %d changes, %d with a quote not in the article; tokens in %d, out %d, thinking %d",
		st.Asked, st.Skipped, st.Failed, st.Changes, st.Invalid, st.Usage.In, st.Usage.Out, st.Usage.Thinking)
	if err != nil {
		log.Fatal(err)
	}
}
