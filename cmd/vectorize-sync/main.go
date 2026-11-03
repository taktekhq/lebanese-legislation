// vectorize-sync applies an NDJSON or Parquet export using a hash manifest.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/taktekhq/lebanese-legislation/internal/cli"
	"github.com/taktekhq/lebanese-legislation/internal/vectorize"
)

func main() { ctx, cancel := cli.Context(); defer cancel(); cli.Exit(run(ctx)) }

type job struct {
	vectors []vectorize.Vector
	ids     []string
}

func run(ctx context.Context) error {
	input := flag.String("in", "vectors.ndjson", "input NDJSON or Parquet file (Parquet requires DuckDB)")
	prev := flag.String("prev", "", "previous id<TAB>hash manifest (missing file means first run)")
	manifest := flag.String("manifest", "vectors-manifest.tsv", "new manifest output")
	dry := flag.Bool("dry-run", false, "print counts without changing Vectorize or writing a manifest")
	flag.Parse()
	previous, err := readManifest(*prev)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var f io.ReadCloser
	var duckdb *exec.Cmd
	if strings.HasSuffix(*input, ".parquet") {
		query := "SET memory_limit='2GB'; SET threads=2; SET preserve_insertion_order=false; " +
			"COPY (SELECT id, document, model, hash, values FROM read_parquet('" +
			strings.ReplaceAll(*input, "'", "''") + "')) TO '/dev/stdout' (FORMAT JSON, ARRAY false);"
		duckdb = exec.CommandContext(ctx, "duckdb", "-batch", "-c", query)
		duckdb.Stderr = os.Stderr
		f, err = duckdb.StdoutPipe()
		if err != nil {
			return err
		}
		if err = duckdb.Start(); err != nil {
			f.Close()
			return err
		}
		defer func() {
			if duckdb.ProcessState == nil {
				cancel()
				f.Close()
				duckdb.Wait()
			}
		}()
	} else {
		f, err = os.Open(*input)
		if err != nil {
			return err
		}
	}
	defer f.Close()
	var client *vectorize.Client
	if !*dry {
		client, err = vectorize.New(os.Getenv("CLOUDFLARE_ACCOUNT_ID"), os.Getenv("LEBLEG_VECTORIZE_INDEX"), os.Getenv("LEBLEG_VECTORIZE_TOKEN"))
		if err != nil {
			return err
		}
	}
	jobs := make(chan job, 4)
	var wg sync.WaitGroup
	var once sync.Once
	var uploadErr error
	for range 4 {
		wg.Go(func() {
			for j := range jobs {
				if ctx.Err() != nil {
					continue
				}
				var err error
				if len(j.vectors) > 0 {
					err = client.Upsert(ctx, j.vectors)
				} else {
					err = client.DeleteByIDs(ctx, j.ids)
				}
				if err != nil {
					once.Do(func() { uploadErr = err; cancel() })
				}
			}
		})
	}
	// Always join workers before returning, including invalid input and disk errors.
	joined := false
	defer func() {
		if !joined {
			cancel()
			close(jobs)
			wg.Wait()
		}
	}()
	send := func(j job) error {
		if *dry {
			return nil
		}
		select {
		case jobs <- j:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var tmp *os.File
	var writer *bufio.Writer
	if !*dry {
		tmp, err = os.CreateTemp(filepath.Dir(*manifest), ".vectors-manifest-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		// CreateTemp makes the file 0600; the workflow uploads it from outside the container's user.
		if err = tmp.Chmod(0o644); err != nil {
			return err
		}
		writer = bufio.NewWriter(tmp)
	}
	current := make(map[string]string)
	dec := json.NewDecoder(bufio.NewReaderSize(f, 1<<20))
	var batch []vectorize.Vector
	total, upserts, unchanged := 0, 0, 0
	for {
		var row struct {
			ID     string    `json:"id"`
			Hash   string    `json:"hash"`
			Values []float32 `json:"values"`
		}
		if err = dec.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("input row %d: %w", total+1, err)
		}
		if row.ID == "" || len(row.ID) > 64 || strings.ContainsAny(row.ID, "\t\r\n") || row.Hash == "" || strings.ContainsAny(row.Hash, "\t\r\n") || len(row.Values) != 768 {
			return fmt.Errorf("invalid vector at row %d", total+1)
		}
		if _, exists := current[row.ID]; exists {
			return fmt.Errorf("duplicate vector id %q", row.ID)
		}
		current[row.ID] = row.Hash
		total++
		if writer != nil {
			if _, err = fmt.Fprintf(writer, "%s\t%s\n", row.ID, row.Hash); err != nil {
				return err
			}
		}
		if old, exists := previous[row.ID]; exists && old == row.Hash {
			unchanged++
			continue
		}
		upserts++
		if !*dry {
			batch = append(batch, vectorize.Vector{ID: row.ID, Values: row.Values})
		}
		if len(batch) == 1000 {
			if err = send(job{vectors: batch}); err != nil {
				return err
			}
			batch = nil
		}
	}
	if duckdb != nil {
		if err = duckdb.Wait(); err != nil {
			return fmt.Errorf("read Parquet: %w", err)
		}
	}
	if len(batch) > 0 {
		if err = send(job{vectors: batch}); err != nil {
			return err
		}
	}
	var ids []string
	deletes := 0
	for id := range previous {
		if _, exists := current[id]; exists {
			continue
		}
		deletes++
		if !*dry {
			ids = append(ids, id)
		}
		if len(ids) == 1000 {
			if err = send(job{ids: ids}); err != nil {
				return err
			}
			ids = nil
		}
	}
	if len(ids) > 0 {
		if err = send(job{ids: ids}); err != nil {
			return err
		}
	}
	// Close and join here so a failed upload can never publish a new manifest.
	close(jobs)
	wg.Wait()
	joined = true
	if uploadErr != nil {
		return uploadErr
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if !*dry {
		if err = writer.Flush(); err != nil {
			return err
		}
		if err = tmp.Sync(); err != nil {
			return err
		}
		if err = tmp.Close(); err != nil {
			return err
		}
		if err = os.Rename(tmp.Name(), *manifest); err != nil {
			return err
		}
	}
	fmt.Printf("total=%d upsert=%d unchanged=%d delete=%d\n", total, upserts, unchanged, deletes)
	return nil
}

func readManifest(path string) (map[string]string, error) {
	out := make(map[string]string)
	if path == "" {
		return out, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		id, hash, ok := strings.Cut(scanner.Text(), "\t")
		if !ok || id == "" || len(id) > 64 || hash == "" || strings.ContainsAny(hash, "\t\r\n") {
			return nil, fmt.Errorf("invalid manifest row %q", scanner.Text())
		}
		if _, exists := out[id]; exists {
			return nil, fmt.Errorf("duplicate manifest id %q", id)
		}
		out[id] = hash
	}
	return out, scanner.Err()
}
