// citeagy reads amending and citing articles, and rulings, through the
// Antigravity CLI (`agy`, Gemini on a Google AI subscription), the Claude
// Code CLI (`claude -p`, on a Claude subscription) or the Claude API
// (-api, paid, optionally as a Message Batch at half price), in batches, into
// the same kind of citations file `cite` writes. Same prompt and
// checks; see internal/cite.
//
//	go run ./cmd/citeagy -sample all -batch 25 -workers 2
//	go run ./cmd/citeagy -claude ~/.local/bin/claude -source rulings -sample test -workers 4
//	go run ./cmd/citeagy -api -source rulings -estimate                  # counts tokens, asks nothing
//	go run ./cmd/citeagy -api -source rulings -limit 100 -workers 4      # ANTHROPIC_API_KEY in the environment
//	go run ./cmd/citeagy -api -batch-api -source rulings                 # all of them, as a Message Batch
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/cite"
)

// backend picks the model: Claude when claudeBin is set, else agy.
func backend(ctx context.Context, claudeBin, claudeRemote, model string, timeout time.Duration, a *cite.Agy) (cite.Backend, func(), error) {
	if claudeBin == "" {
		a.Model = model
		return a, func() {}, nil
	}
	c, err := cite.NewClaude(ctx, claudeBin, claudeRemote, model, timeout)
	if err != nil {
		return nil, nil, err
	}
	return c, c.Cleanup, nil
}

// apiBackend sets up the Claude API reader from the flags. The client takes
// its key from ANTHROPIC_API_KEY (and a base URL from ANTHROPIC_BASE_URL); it
// is left out without a key, which only an -estimate can do without.
func apiBackend(model, effort string, batchAPI, estimate bool, workers int, maxTokens int64, poll time.Duration, usageLog, batchLog, batchID string) *cite.API {
	if model == "" {
		model = cite.DefaultAPIModel
	}
	a := &cite.API{Model: model, Effort: effort, Batch: batchAPI, Estimate: estimate, Workers: workers, MaxTokens: maxTokens,
		Poll: poll, UsageLog: usageLog, BatchLog: batchLog, BatchID: batchID}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		c := anthropic.NewClient()
		a.Client = &c
	}
	return a
}

func main() {
	defaultDSN := "host=/tmp dbname=lebleg"
	if v := os.Getenv("LEBLEG_DSN"); v != "" {
		defaultDSN = v
	}
	dsn := flag.String("db", defaultDSN, "Postgres connection string (default LEBLEG_DSN, else the local database)")
	source := flag.String("source", "articles", `what to read: "articles" (law articles that change or cite a text) or "rulings" (the texts a ruling applies, cites or interprets)`)
	sample := flag.String("sample", "all", `"test" (a fixed 200), "all", or for rulings "short" (summaries of 500 characters or less)`)
	out := flag.String("out", "", "where the citations are kept (default db/model/citations.jsonl, or db/model/ruling_citations.jsonl for rulings)")
	bin := flag.String("agy", os.ExpandEnv("$HOME/.local/bin/agy"), "path to the agy CLI")
	remote := flag.String("remote", "", "run agy on this ssh host (e.g. builder) instead of here")
	remoteBin := flag.String("remote-bin", "agy", "the agy command on the remote host (a wrapper selects another login)")
	claudeBin := flag.String("claude", "", "path to the claude CLI: when set, read with Claude instead of agy")
	claudeRemote := flag.String("claude-remote", "", "run claude on this ssh host (e.g. builder) instead of here")
	model := flag.String("model", "", `model: an agy model (default "Gemini 3.8 Flash (Low)"), with -claude a Claude alias (default "sonnet"), with -api a model id (default "claude-opus-5-5")`)
	useAPI := flag.Bool("api", false, "read with the Claude API (ANTHROPIC_API_KEY in the environment; paid) instead of agy or claude")
	effort := flag.String("effort", "low", "with -api: output_config.effort (low, medium, high, xhigh, max)")
	batchAPI := flag.Bool("batch-api", false, "with -api: submit as a Message Batch (half price), poll until it ends, collect the results")
	estimate := flag.Bool("estimate", false, "with -api: count the input tokens of 20 sampled requests (free) and project them to the whole selection; asks the model nothing")
	maxTokens := flag.Int64("max-tokens", 16000, "with -api: most tokens of one answer")
	poll := flag.Duration("poll", 30*time.Second, "with -batch-api: how often to look at the batch")
	usageLog := flag.String("usage-log", "_ctx/api-usage.jsonl", "with -api: where each request's tokens are logged")
	batchLog := flag.String("batch-log", "_ctx/api-batches.jsonl", "with -batch-api: where submitted batches are noted, so -batch-id can collect one")
	batchID := flag.String("batch-id", "", "with -batch-api: collect this earlier batch (from -batch-log) instead of submitting a new one")
	batch := flag.Int("batch", 0, "items per call (default 25 articles, 8 rulings)")
	workers := flag.Int("workers", 8, "sessions in parallel (agy text calls are fine at 16)")
	limit := flag.Int("limit", 0, "read at most this many (0: all)")
	redo := flag.String("redo", "", `ask again about items a model whose name contains this answered; "invalid:sonnet" only those with a quote not in the text; a leading "keep:" keeps the earlier answer's valid citations beside the new ones`)
	timeout := flag.Duration("timeout", 10*time.Minute, "per call")
	flag.Parse()
	src, err := cite.SourceByName(*source)
	if err != nil {
		log.Fatal(err)
	}
	if *out == "" {
		*out = "db/model/citations.jsonl"
		if src.Name == "rulings" {
			*out = "db/model/ruling_citations.jsonl"
		}
	}
	if *batch == 0 {
		*batch = 25
		if src.Name == "rulings" {
			*batch = 8
		}
	}
	if *useAPI && (*claudeBin != "" || *remote != "") {
		log.Fatal("-api reads through the API: do not combine it with -claude or -remote")
	}
	if (*batchAPI || *estimate || *batchID != "") && !*useAPI {
		log.Fatal("-batch-api, -estimate and -batch-id need -api")
	}
	if *model == "" && *claudeBin == "" && !*useAPI {
		*model = "Gemini 3.8 Flash (Low)"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatal(err)
	}
	var items []cite.Item
	if src.Name == "rulings" {
		items, err = cite.RulingItems(ctx, pool, *sample)
	} else {
		items, err = cite.Items(ctx, pool, *sample)
	}
	pool.Close()
	if err != nil {
		log.Fatal(err)
	}
	if *useAPI {
		api := apiBackend(*model, *effort, *batchAPI, *estimate, *workers, *maxTokens, *poll, *usageLog, *batchLog, *batchID)
		start := time.Now()
		st, err := cite.RunAPI(ctx, api, src, items, *out, *batch, *limit, *redo, func(f string, x ...any) { log.Printf(f, x...) })
		log.Printf("read %d, skipped %d, failed %d; %d changes, %d with a quote not in the text; %d results, %s",
			st.Asked, st.Skipped, st.Failed, st.Changes, st.Invalid, st.Calls, time.Since(start).Round(time.Second))
		if err != nil {
			log.Fatal(err)
		}
		if st.Asked == 0 && st.Failed > 0 {
			os.Exit(3)
		}
		return
	}
	a := &cite.Agy{Remote: *remote, RemoteBin: *remoteBin, Bin: *bin, Timeout: *timeout}
	be, cleanup, err := backend(ctx, *claudeBin, *claudeRemote, *model, *timeout, a)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()
	start := time.Now()
	st, err := cite.RunBatches(ctx, be, src, items, *out, *workers, *batch, *limit, *redo, func(f string, x ...any) { log.Printf(f, x...) })
	log.Printf("read %d, skipped %d, failed %d; %d changes, %d with a quote not in the text; %d calls, %d characters of text, %d sent, %s",
		st.Asked, st.Skipped, st.Failed, st.Changes, st.Invalid, st.Calls, st.TextChars, st.PromptChars, time.Since(start).Round(time.Second))
	if err != nil {
		log.Fatal(err)
	}
	// Nonzero when nothing could be read at all (a quota that ran out), so the
	// nightly can tell and try the other model.
	if st.Asked == 0 && st.Failed > 0 {
		cleanup()
		os.Exit(3)
	}
}
