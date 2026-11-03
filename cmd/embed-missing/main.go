// embed-missing embeds an explicit id,hash CSV and writes export-vectors NDJSON.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/embedding"
	"github.com/taktekhq/lebanese-legislation/internal/release"
	"github.com/taktekhq/lebanese-legislation/internal/vectorize"
)

type missingChunk struct{ ID, Hash string }

// readMissing checks the entire request and its cap before constructing a provider.
func readMissing(r io.Reader, cap int) ([]missingChunk, error) {
	if cap < 0 {
		return nil, errors.New("-max-new must not be negative")
	}
	reader := csv.NewReader(r)
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("missing CSV header: %w", err)
	}
	if len(header) != 2 || header[0] != "id" || header[1] != "hash" {
		return nil, errors.New("missing CSV must have header id,hash")
	}
	var chunks []missingChunk
	seen := make(map[string]bool)
	for {
		row, err := reader.Read()
		if err == io.EOF {
			return chunks, nil
		}
		if err != nil {
			return nil, err
		}
		if row[0] == "" || row[1] == "" || seen[row[0]] {
			return nil, errors.New("missing CSV has empty fields or duplicate id")
		}
		seen[row[0]] = true
		chunks = append(chunks, missingChunk{row[0], row[1]})
		if len(chunks) > cap {
			return nil, fmt.Errorf("missing embeddings exceed explicit cap %d", cap)
		}
	}
}

type budgetStopped struct{ error }

func (budgetStopped) ExitCode() int { return 2 }

func main() { ctx, cancel := cli.Context(); defer cancel(); cli.Exit(run(ctx)) }

func run(ctx context.Context) error {
	dsn := flag.String("index-dsn", os.Getenv("SEARCH_DATABASE_URL"), "metadata database DSN")
	missing := flag.String("missing", "", "input id,hash CSV")
	out := flag.String("out", "vectors.ndjson", "output NDJSON, in export-vectors format")
	maxNew := flag.Int("max-new", 20000, "refuse before any request above this missing row count")
	dailyDir := flag.String("daily-budget-dir", ".release/budget", "shared daily ledger directory")
	dailyUSD := flag.String("daily-usd", "1", "daily dollar cap")
	price := flag.String("usd-per-million", "0.15", "dollars per million input tokens")
	zone := flag.String("timezone", "Asia/Beirut", "budget day time zone")
	workers := flag.Int("workers", 2, "concurrent embedding requests, 1 to 8")
	batch := flag.Int("batch", 32, "texts per request, 1 to 100")
	flag.Parse()
	if *workers < 1 || *workers > 8 || *batch < 1 || *batch > 100 {
		return errors.New("-workers must be 1 to 8 and -batch 1 to 100")
	}
	if *dailyDir == "" {
		return errors.New("-daily-budget-dir must not be empty")
	}
	input, err := os.Open(*missing)
	if err != nil {
		return err
	}
	chunks, err := readMissing(input, *maxNew)
	input.Close()
	if err != nil {
		return err
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(chunks) == 0 {
		fmt.Println("embedded=0")
		return f.Close()
	}
	conn, err := pgx.Connect(ctx, *dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	ids, hashes := make([]string, len(chunks)), make([]string, len(chunks))
	for i, c := range chunks {
		ids[i], hashes[i] = c.ID, c.Hash
	}
	rows, err := conn.Query(ctx, `SELECT m.id,c.kind || ':' || c.source_id::text,c.model,c.hash,c.text
  FROM semantic_chunks c JOIN unnest($1::text[],$2::text[]) AS m(id,wanted_hash)
  ON `+vectorize.ChunkIDSQL+`=m.id AND c.hash=m.wanted_hash
  WHERE c.model=$3 ORDER BY m.id`, ids, hashes, release.Model)
	if err != nil {
		return err
	}
	type chunk struct{ ID, Document, Model, Hash, Text string }
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (chunk, error) {
		var c chunk
		err := r.Scan(&c.ID, &c.Document, &c.Model, &c.Hash, &c.Text)
		return c, err
	})
	if err != nil {
		return err
	}
	if len(items) != len(chunks) {
		return fmt.Errorf("found %d of %d missing chunks with matching id, hash and model", len(items), len(chunks))
	}
	gemini, err := embedding.NewGemini(os.Getenv("GEMINI_API_KEY"), embedding.Config{})
	if err != nil {
		return err
	}
	provider, err := embedding.NewDaily(gemini, *dailyDir, *dailyUSD, *price, *zone)
	if err != nil {
		return err
	}
	type result struct {
		items   []chunk
		vectors [][]float32
		err     error
	}
	results := make(chan result, *workers)
	var stopped atomic.Bool
	var failure error
	pending, next, count := 0, 0, 0
	enc := json.NewEncoder(f)
	save := func(r result) error {
		if r.err != nil {
			return r.err
		}
		if len(r.vectors) != len(r.items) {
			return errors.New("embedding response count mismatch")
		}
		for _, values := range r.vectors {
			if len(values) != release.Dimensions {
				return errors.New("embedding dimensions mismatch")
			}
		}
		for i, c := range r.items {
			row := struct {
				ID       string    `json:"id"`
				Document string    `json:"document"`
				Model    string    `json:"model"`
				Hash     string    `json:"hash"`
				Values   []float32 `json:"values"`
			}{c.ID, c.Document, c.Model, c.Hash, r.vectors[i]}
			if err := enc.Encode(row); err != nil {
				return err
			}
			count++
		}
		return f.Sync()
	}
	for next < len(items) || pending > 0 {
		for next < len(items) && pending < *workers && !stopped.Load() {
			end := min(next+*batch, len(items))
			batchItems := items[next:end]
			next = end
			pending++
			go func() {
				texts := make([]string, len(batchItems))
				// Match releasedb.fillMissing: title is not prepended.
				for i, c := range batchItems {
					texts[i] = c.Text
				}
				vectors, err := provider.Embed(ctx, texts)
				if err != nil {
					stopped.Store(true)
				}
				results <- result{batchItems, vectors, err}
			}()
		}
		if pending == 0 {
			break
		}
		err := save(<-results)
		pending--
		if err != nil {
			stopped.Store(true)
			if failure == nil || (errors.Is(failure, embedding.ErrBudget) && !errors.Is(err, embedding.ErrBudget)) {
				failure = err
			}
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("embedded=%d missing=%d\n", count, len(items)-count)
	if errors.Is(failure, embedding.ErrBudget) {
		return budgetStopped{failure}
	}
	return failure
}
