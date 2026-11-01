// Command ruling-ocr routes each judgment page to Tesseract, Claude or Gemini and reads it.
//
//	ruling-ocr -stage triage|tesseract|blank|claude|claude-api|gemini|merge [-dir dir] [-cache dir] [-meta tsv] [-budget usd]
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/rulingfile"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	stage := flag.String("stage", "", "triage, blank (mark pages with no ink), claude, gemini, or merge (vote again from stored readings)")
	dir := flag.String("dir", "ruling-files", "the folder ruling-files wrote")
	cache := flag.String("cache", "ruling-cache", "rendered page images")
	meta := flag.String("meta", "", "TSV of id, court, number, year, president, for checking readings")
	courts := flag.String("important-courts", "المجلس الدستوري,المجلس العدلي", "these courts' rulings get Gemini as a fourth reader on every page")
	first := flag.String("first", "", "file of ruling ids (one per line) to read before the rest")
	workers := flag.Int("workers", 4, "")
	chunk := flag.Int("chunk", 8, "pages per call")
	dpi := flag.Int("dpi", 200, "")
	watch := flag.Duration("watch", 0, "repeat this often (0: once)")
	haikuConf := flag.Int("haiku-conf", rulingfile.DefaultRouter.HaikuConf, "Tesseract confidence for Haiku")
	sonnetConf := flag.Int("sonnet-conf", rulingfile.DefaultRouter.SonnetConf, "Tesseract confidence for Sonnet (below: Gemini)")
	claudeBin := flag.String("claude", "claude", "")
	creds := flag.String("credentials", os.ExpandEnv("$HOME/.claude/.credentials.json"), "Claude Code's login, for the usage guard")
	max5h := flag.Float64("max-5h", 85, "pause Claude above this share of the 5-hour limit")
	maxWeek := flag.Float64("max-week", 85, "pause Claude above this share of the weekly limit")
	onlyUnread := flag.Bool("only-unread", false, "-stage claude: read only pages no model has read yet, never a second reading")
	model := flag.String("gemini-model", "gemini-3.8-flash", "")
	priceIn := flag.Float64("price-in", 0.375, "USD per million input tokens (batch)")
	priceOut := flag.Float64("price-out", 1.875, "USD per million output tokens (batch)")
	est := flag.Float64("est-page", 0.0017, "USD reserved per page in flight")
	budget := flag.Float64("budget", 0, "USD, the most the Gemini stage may spend in all (required for -stage gemini)")
	maxPages := flag.Int("max-pages", 800, "pages per batch (each about 0.25 MB to upload)")
	maxFlight := flag.Int("max-flight", 2, "batches in flight")
	inlineMB := flag.Int("inline-mb", 0, "-stage gemini: send up to this many MB of requests inside the batch call instead of uploading a file (0: upload)")
	claudeModel := flag.String("claude-model", "haiku", "-stage claude-api: haiku, sonnet or opus")
	claudeEst := flag.Float64("claude-est-page", 0, "-stage claude-api: USD reserved per page in flight (0: from the model's price)")
	maxMB := flag.Int("max-mb", 60, "-stage claude-api: MB of PDF per batch (the house uploads about 0.35 MB/s)")
	flag.Parse()
	// Readers get absolute page paths: a relative one sends Claude hunting for the file.
	for _, p := range []*string{dir, cache} {
		if abs, err := filepath.Abs(*p); err == nil {
			*p = abs
		}
	}
	router := rulingfile.DefaultRouter
	router.HaikuConf, router.SonnetConf = *haikuConf, *sonnetConf
	var m map[int]rulingfile.Meta
	if *meta != "" {
		var err error
		if m, err = rulingfile.LoadMeta(*meta); err != nil {
			log.Fatal(err)
		}
		router.Important = rulingfile.ImportantIDs(m, strings.Split(*courts, ","))
		log.Printf("ruling-ocr: %d rulings on record, %d important", len(m), len(router.Important))
	}
	if *first != "" {
		b, err := os.ReadFile(*first)
		if err != nil {
			log.Fatal(err)
		}
		router.First = map[int]bool{}
		for _, f := range strings.Fields(string(b)) {
			var id int
			if _, err := fmt.Sscan(f, &id); err == nil {
				router.First[id] = true
			}
		}
	}
	var run func() error
	switch *stage {
	case "triage":
		t := &rulingfile.Triager{Dir: *dir, Cache: *cache, DPI: *dpi, Workers: *workers, Router: router, Meta: m, Log: log.Printf}
		run = func() error { return t.Run(ctx) }
	case "tesseract":
		t := &rulingfile.Triager{Dir: *dir, Cache: *cache, DPI: *dpi, Workers: *workers, Router: router, Meta: m, Log: log.Printf}
		run = func() error { return t.Backfill(ctx) }
	case "blank":
		t := &rulingfile.Triager{Dir: *dir, Cache: *cache, Router: router, Meta: m, Log: log.Printf}
		run = t.MarkBlank
	case "merge":
		run = func() error { return router.Remerge(*dir, m, log.Printf) }
	case "claude":
		if m == nil {
			log.Fatal("-stage claude needs -meta, to check readings")
		}
		c := &rulingfile.ClaudeReader{Dir: *dir, Cache: *cache, Bin: *claudeBin, Workers: *workers, ChunkPages: *chunk,
			Timeout: 15 * time.Minute, Router: router, Meta: m, Log: log.Printf, OnlyUnread: *onlyUnread,
			Guard: &rulingfile.UsageGuard{Credentials: *creds, Max5h: *max5h, MaxWeek: *maxWeek, Log: log.Printf}}
		run = func() error { return c.Run(ctx) }
	case "gemini":
		if *inlineMB > 19 {
			log.Fatal("-inline-mb: Google takes at most 20 MB per batch call; 19 or less")
		}
		key := os.Getenv("GEMINI_API_KEY")
		if key == "" || *budget <= 0 || m == nil {
			log.Fatal("-stage gemini needs GEMINI_API_KEY, -budget and -meta")
		}
		g := &rulingfile.GeminiBatch{Dir: *dir, Cache: *cache, Key: key, Model: *model, PriceIn: *priceIn, PriceOut: *priceOut,
			EstPerPage: *est, Budget: *budget, MaxPages: *maxPages, MaxFlight: *maxFlight, MaxTries: 3, ChunkPages: *chunk, MaxInlineBytes: *inlineMB << 20,
			Router: router, Meta: m, Log: log.Printf, HTTP: &http.Client{Timeout: 10 * time.Minute}}
		run = func() error { return g.Step(ctx) }
	case "claude-api":
		model := map[string]string{"haiku": rulingfile.Haiku, "sonnet": rulingfile.Sonnet, "opus": rulingfile.Opus}[*claudeModel]
		if os.Getenv("ANTHROPIC_API_KEY") == "" || *budget <= 0 || m == nil || model == "" {
			log.Fatal("-stage claude-api needs ANTHROPIC_API_KEY, -budget, -meta and -claude-model haiku|sonnet|opus")
		}
		est := *claudeEst
		if est <= 0 { // about 2,500 tokens in and 1,500 out per page, with room
			p := rulingfile.ClaudeBatchPrice[model]
			est = 1.5 * (2500*p[0] + 1500*p[1]) / 1e6
		}
		client := anthropic.NewClient()
		c := &rulingfile.ClaudeBatch{Dir: *dir, Client: &client, Model: model, EstPerPage: est, Budget: *budget,
			MaxPages: *maxPages, MaxBytes: *maxMB << 20, MaxFlight: *maxFlight, MaxTries: 3, ChunkPages: *chunk,
			Router: router, Meta: m, Log: log.Printf}
		run = func() error { return c.Step(ctx) }
	default:
		log.Fatal("-stage is triage, tesseract, blank, claude, claude-api, gemini or merge")
	}
	for {
		if err := run(); err != nil && ctx.Err() == nil {
			log.Printf("ruling-ocr %s: %v", *stage, err)
			if *watch == 0 {
				os.Exit(1)
			}
		}
		if *watch == 0 || ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(*watch):
		case <-ctx.Done():
		}
	}
}
