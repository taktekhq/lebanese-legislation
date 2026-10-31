package semantic

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db       *pgxpool.Pool
	embedder Embedder
	model    string
}

func NewStore(pool *pgxpool.Pool, embedder Embedder) *Store {
	model := DefaultModel
	if m, ok := embedder.(interface{ Model() string }); ok {
		model = m.Model()
	}
	return &Store{db: pool, embedder: embedder, model: model}
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	if err := s.EnsureStorage(ctx); err != nil {
		return err
	}
	return s.BuildIndex(ctx)
}

// EnsureStorage creates storage without creating or dropping an ANN index.
// Initial offline loads can defer ANN construction until their vectors are saved.
func (s *Store) EnsureStorage(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector;
 CREATE TABLE IF NOT EXISTS public.semantic_chunks (
 model text NOT NULL,kind text NOT NULL,source_id integer NOT NULL,part text NOT NULL,lang text NOT NULL,
 title text NOT NULL,text text NOT NULL,hash text NOT NULL,chunk_start integer NOT NULL,chunk_end integer NOT NULL,
 embedding halfvec(768) NOT NULL,updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(model,kind,source_id,part,lang,hash,chunk_start),
 CHECK(chunk_start>=0 AND chunk_end>chunk_start AND chunk_end-chunk_start<=900));
 CREATE TABLE IF NOT EXISTS public.semantic_documents (
 model text NOT NULL,kind text NOT NULL,source_id integer NOT NULL,part text NOT NULL,lang text NOT NULL,
 hash text NOT NULL,seen_run text NOT NULL,PRIMARY KEY(model,kind,source_id,part,lang));

 CREATE INDEX IF NOT EXISTS semantic_chunks_source ON public.semantic_chunks(model,kind,source_id,part,lang);`)
	if err != nil {
		return err
	}
	var storage string
	if err = s.db.QueryRow(ctx, `SELECT format_type(atttypid,atttypmod) FROM pg_attribute WHERE attrelid='public.semantic_chunks'::regclass AND attname='embedding' AND NOT attisdropped`).Scan(&storage); err != nil {
		return err
	}
	if storage != "halfvec(768)" {
		return &StorageError{Type: storage}
	}

	return err
}

// StorageError means an existing index needs an explicit offline migration.
// Storage is never rewritten automatically during normal schema setup.
type StorageError struct{ Type string }

func (e *StorageError) Error() string {
	return fmt.Sprintf("semantic index uses %s; expected halfvec(768); migrate existing embeddings offline before indexing or serving", e.Type)
}

// BuildIndex creates the serving ANN index if absent. It never drops an index.
func (s *Store) BuildIndex(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS semantic_chunks_embedding ON public.semantic_chunks USING hnsw(embedding halfvec_cosine_ops)`)
	return err
}

func (s *Store) Search(ctx context.Context, q string, limit int) ([]Hit, error) {
	if s.embedder == nil || s.db == nil {
		return nil, errors.New("semantic search unavailable")
	}
	if strings.TrimSpace(q) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 12
	}
	limit = min(limit, 100)
	vs, err := s.embedder.Embed(ctx, []string{q}, true)
	if err != nil {
		return nil, err
	}
	if len(vs) != 1 {
		return nil, errors.New("embedding response count mismatch")
	}
	v, err := normalize(vs[0])
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, `SET LOCAL hnsw.iterative_scan = 'strict_order'; SET LOCAL hnsw.ef_search = 100; SET LOCAL hnsw.max_scan_tuples = 20000`); err != nil {
		return nil, err
	}
	query := func(sql string) ([]Hit, error) {
		rows, err := tx.Query(ctx, sql, vectorText(v), s.model, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Hit
		for rows.Next() {
			var h Hit
			if err = rows.Scan(&h.Kind, &h.ID, &h.Part, &h.Lang, &h.Title, &h.Text, &h.Start, &h.End, &h.Hash, &h.Distance); err != nil {
				return nil, err
			}
			out = append(out, h)
		}
		return out, rows.Err()
	}
	const columns = `kind,source_id,part,lang,title,text,chunk_start,chunk_end,hash,embedding <=> $1::halfvec AS distance`
	out, err := query(`SELECT ` + columns + ` FROM public.semantic_chunks WHERE model=$2 ORDER BY embedding <=> $1::halfvec LIMIT $3`)
	if err != nil {
		return nil, err
	}
	if len(out) < limit {
		// A model can be a tiny minority of a shared ANN graph. The bounded ANN
		// scan may exhaust its candidates before its model filter fills the limit.
		// Materialization ensures an exact, indexed-model fallback cannot reuse HNSW.
		out, err = query(`WITH model_distances AS MATERIALIZED (SELECT ctid AS row_id,embedding <=> $1::halfvec AS distance FROM public.semantic_chunks WHERE model=$2), closest AS (SELECT row_id,distance FROM model_distances ORDER BY distance LIMIT $3) SELECT ` + columns + ` FROM closest JOIN public.semantic_chunks ON semantic_chunks.ctid=closest.row_id ORDER BY closest.distance`)
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil

}

func vectorText(v []float32) string {
	var b strings.Builder
	b.Grow(len(v) * 12)
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

type SyncOptions struct {
	Progress  func(Stats)   // Serialized callback after each completed document group.
	Limit     int           // Maximum canonical documents, zero means all.
	Offset    int           // Deterministic corpus document offset.
	BatchSize int           // Texts in each embedding call, default 100, maximum 100.
	GroupSize int           // Documents queued per worker group, default 100, maximum 1000.
	Workers   int           // Concurrent document workers, default 1, maximum 32.
	Rate      time.Duration // Minimum interval between embedding batch starts across workers.
}

type Stats struct{ Documents, Chunks, Embedded, Skipped, Pruned int }

func (s *Store) Sync(ctx context.Context, corpus *pgxpool.Pool, opts SyncOptions) (Stats, error) {
	var total Stats
	if corpus == nil || s.db == nil || s.embedder == nil {
		return total, errors.New("semantic sync unavailable")
	}
	if opts.Limit < 0 || opts.Offset < 0 || opts.Rate < 0 || opts.GroupSize < 0 || opts.GroupSize > 1000 {
		return total, errors.New("invalid sync bounds")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	opts.BatchSize = min(opts.BatchSize, 100)
	if opts.GroupSize == 0 {
		opts.GroupSize = 100
	}
	if opts.Workers <= 0 {
		opts.Workers = 1
	}
	opts.Workers = min(opts.Workers, 32)
	// A session lock serializes all runs of a model, including partial runs. A
	// dedicated connection avoids occupying a slot needed by indexing workers.
	lock, err := pgx.ConnectConfig(ctx, s.db.Config().ConnConfig.Copy())
	if err != nil {
		return total, err
	}
	defer func() { _ = lock.Close(context.Background()) }()
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('lebleg-semantic:' || $1,0))`, s.model); err != nil {
		return total, err
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return total, err
	}
	run := hex.EncodeToString(token[:])

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var firstErr error
	var next time.Time
	var rateMu sync.Mutex
	waitRate := func() error {
		if opts.Rate == 0 {
			return nil
		}
		rateMu.Lock()
		at := maxTime(time.Now(), next)
		next = at.Add(opts.Rate)
		rateMu.Unlock()
		timer := time.NewTimer(time.Until(at))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	jobs := make(chan []Document, opts.Workers)
	var wg sync.WaitGroup
	for range opts.Workers {
		wg.Go(func() {
			for d := range jobs {
				stats, err := s.syncDocuments(ctx, d, opts.BatchSize, waitRate, run)
				mu.Lock()
				total.Documents += stats.Documents
				total.Chunks += stats.Chunks
				total.Embedded += stats.Embedded
				total.Skipped += stats.Skipped
				if opts.Progress != nil && err == nil {
					opts.Progress(total)
				}
				if err != nil && firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
				if err != nil {
					return
				}
			}
		})
	}
	var group []Document
	send := func() error {
		select {
		case jobs <- group:
			group = nil
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	walkErr := walkCorpus(ctx, corpus, opts.Offset, opts.Limit, func(d Document) error {
		group = append(group, d)
		if len(group) >= opts.GroupSize {
			return send()
		}
		return nil
	})
	if walkErr == nil && len(group) > 0 {
		walkErr = send()
	}

	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return total, firstErr
	}
	if walkErr == nil && opts.Offset == 0 && opts.Limit == 0 {
		// Never prune after an interrupted traversal or a bounded/paginated run.
		tx, err := s.db.Begin(ctx)
		if err != nil {
			return total, err
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		result, err := tx.Exec(ctx, `DELETE FROM public.semantic_chunks c WHERE c.model=$1 AND NOT EXISTS (SELECT 1 FROM public.semantic_documents d WHERE d.model=c.model AND d.kind=c.kind AND d.source_id=c.source_id AND d.part=c.part AND d.lang=c.lang AND d.hash=c.hash AND d.seen_run=$2)`, s.model, run)
		if err != nil {
			return total, err
		}
		total.Pruned = int(result.RowsAffected())
		if _, err = tx.Exec(ctx, `DELETE FROM public.semantic_documents WHERE model=$1 AND seen_run<>$2`, s.model, run); err != nil {
			return total, err
		}
		walkErr = tx.Commit(ctx)
	}

	return total, walkErr
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (s *Store) syncDocuments(ctx context.Context, docs []Document, batchSize int, wait func() error, runIDs ...string) (Stats, error) {
	run := ""
	if len(runIDs) > 0 {
		run = runIDs[0]
	}
	stats := Stats{Documents: len(docs)}
	type reference struct {
		Kind string `json:"kind"`
		ID   int    `json:"id"`
		Part string `json:"part"`
		Lang string `json:"lang"`
		Hash string `json:"hash"`
	}
	refs := make([]reference, 0, len(docs))
	var chunks []Hit
	key := func(h Hit) string {
		return fmt.Sprintf("%s:%d:%s:%s:%s:%d", h.Kind, h.ID, h.Part, h.Lang, h.Hash, h.Start)
	}
	for _, d := range docs {
		chunks = append(chunks, Chunks(d)...)
		refs = append(refs, reference{d.Kind, d.ID, d.Part, d.Lang, Hash(d.Text)})
	}
	stats.Chunks = len(chunks)
	if len(chunks) == 0 {
		return stats, nil
	}
	refJSON, _ := json.Marshal(refs)
	rows, err := s.db.Query(ctx, `SELECT c.kind,c.source_id,c.part,c.lang,c.hash,c.chunk_start,c.chunk_end,c.text FROM public.semantic_chunks c JOIN jsonb_to_recordset($2::jsonb) AS r(kind text,id int,part text,lang text,hash text) ON c.kind=r.kind AND c.source_id=r.id AND c.part=r.part AND c.lang=r.lang AND c.hash=r.hash WHERE c.model=$1`, s.model, string(refJSON))
	if err != nil {
		return stats, err
	}
	existing := map[string]string{}
	for rows.Next() {
		var h Hit
		if err = rows.Scan(&h.Kind, &h.ID, &h.Part, &h.Lang, &h.Hash, &h.Start, &h.End, &h.Text); err != nil {
			rows.Close()
			return stats, err
		}
		existing[key(h)] = fmt.Sprint(h.End) + ":" + h.Text
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return stats, err
	}
	var pending []Hit
	for _, h := range chunks {
		if existing[key(h)] == fmt.Sprint(h.End)+":"+h.Text {
			stats.Skipped++
		} else {
			pending = append(pending, h)
		}
	}

	for i := 0; i < len(pending); i += batchSize {
		batch := pending[i:min(i+batchSize, len(pending))]
		texts := make([]string, len(batch))
		for j, h := range batch {
			texts[j] = h.Text
		}
		vectors, embedErr := embedForIndex(ctx, s.embedder, texts, wait)
		if embedErr != nil {
			return stats, embedErr
		}
		if len(vectors) != len(batch) {
			return stats, errors.New("embedding response count mismatch")
		}
		for j := range vectors {
			vectors[j], err = normalize(vectors[j])
			if err != nil {
				return stats, err
			}
		}
		tx, beginErr := s.db.Begin(ctx)
		if beginErr != nil {
			return stats, beginErr
		}
		for j, h := range batch {
			_, err = tx.Exec(ctx, `INSERT INTO public.semantic_chunks(model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::halfvec) ON CONFLICT(model,kind,source_id,part,lang,hash,chunk_start) DO UPDATE SET title=excluded.title,text=excluded.text,chunk_end=excluded.chunk_end,embedding=excluded.embedding,updated_at=now()`, s.model, h.Kind, h.ID, h.Part, h.Lang, h.Title, h.Text, h.Hash, h.Start, h.End, vectorText(vectors[j]))
			if err != nil {
				break
			}
		}
		if err != nil {
			_ = tx.Rollback(context.Background())
			return stats, err
		}
		if err = tx.Commit(ctx); err != nil {
			return stats, err
		}
		stats.Embedded += len(batch)
	}
	// Mark canonical document generations without rewriting unchanged vector
	// rows, since a non-HOT vector-row update would also grow its HNSW graph.
	if _, err = s.db.Exec(ctx, `INSERT INTO public.semantic_documents(model,kind,source_id,part,lang,hash,seen_run) SELECT $1,r.kind,r.id,r.part,r.lang,r.hash,$3 FROM jsonb_to_recordset($2::jsonb) AS r(kind text,id int,part text,lang text,hash text) ON CONFLICT(model,kind,source_id,part,lang) DO UPDATE SET hash=excluded.hash,seen_run=excluded.seen_run`, s.model, string(refJSON), run); err != nil {
		return stats, err
	}

	// Only a fully persisted replacement retires earlier generations. Failure
	// leaves old entries and completed new batches available for a resumable run.
	_, err = s.db.Exec(ctx, `DELETE FROM public.semantic_chunks c USING jsonb_to_recordset($2::jsonb) AS r(kind text,id int,part text,lang text,hash text) WHERE c.model=$1 AND c.kind=r.kind AND c.source_id=r.id AND c.part=r.part AND c.lang=r.lang AND c.hash<>r.hash`, s.model, string(refJSON))
	return stats, err
}

// Count streams the same eligible corpus without calling the embedding service.
func Count(ctx context.Context, corpus *pgxpool.Pool, opts SyncOptions) (Stats, error) {
	var stats Stats
	if opts.Limit < 0 || opts.Offset < 0 {
		return stats, errors.New("invalid count bounds")
	}
	err := walkCorpus(ctx, corpus, opts.Offset, opts.Limit, func(d Document) error { stats.Documents++; stats.Chunks += len(Chunks(d)); return nil })
	return stats, err
}

// Offline indexing retries a failed batch at most three times. Query embeddings
// deliberately remain one-call operations so the website owns its API budget.
func embedForIndex(ctx context.Context, e Embedder, texts []string, wait func() error) ([][]float32, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		if err := wait(); err != nil {
			return nil, err
		}
		vectors, err := e.Embed(ctx, texts, false)
		if err == nil {
			return vectors, nil
		}
		last = err
	}
	return nil, last
}
