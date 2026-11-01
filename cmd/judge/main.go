// Command judge asks a model to judge each recorded change against the amending text (internal/judge).
//
//	judge [-db dsn] [-scope codes|all] [-model m] [-dry]
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
	"github.com/taktekhq/lebanese-legislation/internal/judgecheck"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	// Ask Gemini whether the Centre's text already carries the changes the
	// patterns couldn't place (it judges; it never rewrites); the key
	// comes from the environment, never from a flag or a file here.
	dsn := flag.String("db", cli.DefaultDSN(), "Postgres connection string")
	scope := flag.String("scope", "codes", `"codes" (the principal codes) or "all"`)
	model := flag.String("model", "gemini-pro-latest", "Gemini model")
	out := flag.String("out", "db/model/judgements.jsonl", "where the judgements are kept")
	limit := flag.Int("limit", 0, "ask at most this many (0: all)")
	workers := flag.Int("workers", 4, "requests in parallel")
	dry := flag.Bool("dry", false, "only count what would be asked")
	agy := flag.String("agy", "", "ask Gemini through this agy (Antigravity CLI) binary instead of the API; -model is then an agy model, e.g. gemini-4-argon-medium")
	agyRemote := flag.String("agy-remote", "", "run -agy on this ssh host (where agy is signed in)")
	claude := flag.String("claude", "", "ask Claude through this claude CLI binary instead; -model is then a Claude alias, e.g. sonnet")
	claudeRemote := flag.String("claude-remote", "", "run -claude on this ssh host (where claude is signed in)")
	flag.Parse()
	if *agy != "" && *claude != "" {
		log.Fatal("judge: use -agy or -claude, not both")
	}
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	items, err := judge.Items(ctx, pool, *scope)
	if err != nil {
		log.Fatal(err)
	}
	citations, err := cite.Load("db/model/citations.jsonl")
	if err != nil {
		log.Fatal(err)
	}
	ix, err := judgecheck.CitationIndex(ctx, pool)
	if err != nil {
		log.Fatal(err)
	}
	items, gaps := judgecheck.ResolveItems(ix, items, citations)
	for _, gap := range gaps {
		log.Printf("data gap: %s", gap)
	}
	log.Printf("citation validation: %d items eligible, %d data gaps", len(items), len(gaps))
	chars := 0
	for _, it := range items {
		chars += len([]rune(it.Amending)) + len([]rune(it.Centre))
	}
	log.Printf("%d changes to judge (%s), %.1fM characters of text", len(items), *scope, float64(chars)/1e6)
	if *dry {
		return
	}
	var g judge.Asker
	if *claude != "" {
		g = &judge.Claude{Bin: *claude, Model: *model, Dir: os.TempDir(), Remote: *claudeRemote, Timeout: 5 * time.Minute}
	} else if *agy != "" {
		g = &judge.Agy{Bin: *agy, Model: *model, Dir: os.TempDir(), Remote: *agyRemote, Timeout: 5 * time.Minute}
	} else {
		key := os.Getenv("GEMINI_API_KEY")
		if key == "" {
			log.Fatal("judge: set GEMINI_API_KEY, or use -agy or -claude")
		}
		g = &judge.Gemini{Key: key, Model: *model, HTTP: &http.Client{Timeout: 5 * time.Minute}}
	}
	st, err := judge.Run(ctx, g, items, *out, *workers, *limit, func(f string, a ...any) { log.Printf(f, a...) })
	log.Printf("asked %d, skipped %d, failed %d; states %v; tokens in %d, out %d, thinking %d",
		st.Asked, st.Skipped, st.Failed, st.States, st.Usage.In, st.Usage.Out, st.Usage.Thinking)
	if err != nil {
		log.Fatal(err)
	}
}
