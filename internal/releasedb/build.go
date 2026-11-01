package releasedb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/release"
)

// Embedder makes document vectors (RETRIEVAL_DOCUMENT) for up to 100 texts.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// assertEmpty refuses a target that holds anything but the extensions a
// build creates itself.
func assertEmpty(ctx context.Context, q querier) error {
	var occupied bool
	err := q.QueryRow(ctx, `SELECT
      EXISTS(SELECT 1 FROM pg_namespace WHERE nspname NOT IN ('public','information_schema') AND nspname !~ '^pg_')
      OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        WHERE n.nspname='public' AND c.relkind IN ('r','v','m','p','S','f'))
      OR EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
        WHERE n.nspname='public' AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e'))
      OR EXISTS(SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
        WHERE n.nspname='public' AND t.typcategory<>'A' AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_type'::regclass AND d.objid=t.oid AND d.deptype='e'))
      OR EXISTS(SELECT 1 FROM pg_extension WHERE extname NOT IN ('plpgsql','vector','pg_trgm'))
      OR EXISTS(SELECT 1 FROM pg_largeobject_metadata)
      OR EXISTS(SELECT 1 FROM pg_foreign_server)
      OR EXISTS(SELECT 1 FROM pg_subscription WHERE subdbid=(SELECT oid FROM pg_database WHERE datname=current_database()))`).Scan(&occupied)
	if err != nil {
		return err
	}
	if occupied {
		return errors.New("refusing occupied target database")
	}
	return nil
}

// canonicalChunks is every canonical chunk of every non-blank document, in
// code points: 900-character windows every 780 characters, the last one
// ending at the text's end, as internal/semantic.Chunks cuts them. present
// says whether semantic_chunks holds that exact chunk for the model.
const canonicalChunks = `WITH docs AS (
  SELECT kind,source_id,part,lang,title,text,hash,length(text) AS n FROM source_documents
  WHERE btrim(text,$2)<>''),
chunks AS (
  SELECT d.kind,d.source_id,d.part,d.lang,d.title,d.hash,s AS chunk_start,least(s+900,d.n) AS chunk_end,
    substring(d.text FROM s+1 FOR least(s+900,d.n)-s) AS text
  FROM docs d CROSS JOIN LATERAL generate_series(0,greatest(d.n-121,0),780) s)
SELECT k.*,EXISTS(SELECT 1 FROM semantic_chunks c WHERE c.model=$1 AND (c.kind,c.source_id,c.part,c.lang)=(k.kind,k.source_id,k.part,k.lang)
  AND c.chunk_start=k.chunk_start AND c.chunk_end=k.chunk_end AND c.text=k.text AND c.hash=k.hash) AS present
FROM chunks k`

// Coverage counts canonical chunks and how many have saved vectors.
type Coverage struct {
	Expected int64 `json:"expected"`
	Indexed  int64 `json:"indexed"`
	Missing  int64 `json:"missing"`
}

func coverage(ctx context.Context, q querier) (Coverage, error) {
	var c Coverage
	err := q.QueryRow(ctx, "SELECT count(*),count(*) FILTER (WHERE present) FROM ("+canonicalChunks+") x",
		release.Model, GoWhitespace).Scan(&c.Expected, &c.Indexed)
	c.Missing = c.Expected - c.Indexed
	return c, err
}

// reuseVectors copies saved vectors from another release database (read
// only) and keeps only those whose chunk is canonical for the current text:
// same document, same hash, canonical boundaries, identical excerpt. Only
// the public columns are read: no timestamps, run ids or private titles.
func reuseVectors(ctx context.Context, tx pgx.Tx, source *pgx.Conn) (int64, error) {
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE reuse_candidates(kind text,source_id integer,part text,lang text,
		text text,hash text,chunk_start integer,chunk_end integer,embedding text) ON COMMIT DROP`); err != nil {
		return 0, err
	}
	model := "'" + strings.ReplaceAll(release.Model, "'", "''") + "'"
	query := "COPY (SELECT kind,source_id,part,lang,text,hash,chunk_start,chunk_end,embedding::text FROM public.semantic_chunks WHERE model=" + model + ") TO STDOUT"
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := source.PgConn().CopyTo(ctx, pw, query)
		pw.CloseWithError(err)
		done <- err
	}()
	_, err := tx.Conn().PgConn().CopyFrom(ctx, pr, "COPY reuse_candidates FROM STDIN")
	pr.CloseWithError(err)
	if srcErr := <-done; srcErr != nil && err == nil {
		err = srcErr
	}
	if err != nil {
		return 0, fmt.Errorf("reading reusable vectors: %w", err)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO semantic_chunks(model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding)
		SELECT $1,c.kind,c.source_id,c.part,c.lang,d.title,c.text,d.hash,c.chunk_start,c.chunk_end,c.embedding::halfvec
		FROM reuse_candidates c JOIN source_documents d USING(kind,source_id,part,lang)
		WHERE c.hash=d.hash AND btrim(d.text,$2)<>'' AND c.chunk_start>=0 AND c.chunk_start%780=0
		  AND c.chunk_end=least(c.chunk_start+900,length(d.text)) AND c.chunk_end>c.chunk_start
		  AND (c.chunk_start=0 OR c.chunk_start+120<length(d.text))
		  AND substring(d.text FROM c.chunk_start+1 FOR c.chunk_end-c.chunk_start)=c.text
		ON CONFLICT DO NOTHING`, release.Model, GoWhitespace)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

type missingChunk struct {
	kind, part, lang, title, text, hash string
	sourceID                            int64
	start, end                          int
}

func vectorText(v []float32) string {
	var b strings.Builder
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

// FillOptions bound the paid embedding of missing chunks.
type FillOptions struct {
	Cap       int64 // refuse before any request when more chunks are missing
	Workers   int   // concurrent requests, 1 to 8
	BatchSize int   // texts per request, 1 to 100
}

// fillMissing embeds the missing chunks. Up to Workers requests run at
// once; only this goroutine writes, and each completed batch is committed
// on its own, so paid vectors survive a later failure. After a failure no
// new batch is sent; requests already running are still saved.
func fillMissing(ctx context.Context, conn *pgx.Conn, emb Embedder, o FillOptions) (int64, error) {
	if o.Workers < 1 || o.Workers > 8 {
		return 0, errors.New("embedding workers must be 1 to 8")
	}
	if o.BatchSize < 1 || o.BatchSize > 100 {
		return 0, errors.New("embedding batch size must be 1 to 100")
	}
	cov, err := coverage(ctx, conn)
	if err != nil {
		return 0, err
	}
	if o.Cap < 0 || cov.Missing > o.Cap {
		return 0, fmt.Errorf("missing embeddings %d exceeds explicit cap %d", cov.Missing, o.Cap)
	}
	if cov.Missing == 0 {
		return 0, nil
	}
	if _, err := conn.Exec(ctx, "DECLARE missing_chunks NO SCROLL CURSOR WITH HOLD FOR SELECT kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end FROM ("+
		canonicalChunks+") x WHERE NOT present ORDER BY kind,source_id,part,lang,chunk_start", release.Model, GoWhitespace); err != nil {
		return 0, err
	}
	defer conn.Exec(context.Background(), "CLOSE missing_chunks")
	fetch := func() ([]missingChunk, error) {
		rows, err := conn.Query(ctx, fmt.Sprintf("FETCH %d FROM missing_chunks", o.BatchSize))
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (missingChunk, error) {
			var m missingChunk
			err := r.Scan(&m.kind, &m.sourceID, &m.part, &m.lang, &m.title, &m.text, &m.hash, &m.start, &m.end)
			return m, err
		})
	}
	save := func(items []missingChunk, vectors [][]float32) error {
		if len(vectors) != len(items) {
			return errors.New("embedding response count mismatch")
		}
		batch := &pgx.Batch{}
		for i, m := range items {
			if len(vectors[i]) != release.Dimensions {
				return errors.New("embedding dimensions mismatch")
			}
			batch.Queue(`INSERT INTO semantic_chunks(model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::text::halfvec) ON CONFLICT(model,kind,source_id,part,lang,hash,chunk_start)
				DO UPDATE SET title=excluded.title,text=excluded.text,chunk_end=excluded.chunk_end,embedding=excluded.embedding,updated_at=now()`,
				release.Model, m.kind, m.sourceID, m.part, m.lang, m.title, m.text, m.hash, m.start, m.end, vectorText(vectors[i]))
		}
		return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error { return tx.SendBatch(ctx, batch).Close() })
	}
	type result struct {
		items   []missingChunk
		vectors [][]float32
		err     error
	}
	results := make(chan result, o.Workers)
	var stopped atomic.Bool
	var failure error
	fail := func(err error) {
		stopped.Store(true)
		if failure == nil {
			failure = err
		}
	}
	handle := func(r result) {
		if r.err != nil {
			fail(r.err)
			return
		}
		if err := save(r.items, r.vectors); err != nil {
			fail(err)
		}
	}
	pending := 0
	for !stopped.Load() {
		for pending >= o.Workers {
			handle(<-results)
			pending--
		}
		if stopped.Load() {
			break
		}
		items, err := fetch()
		if err != nil {
			fail(err)
			break
		}
		if len(items) == 0 {
			break
		}
		pending++
		go func(items []missingChunk) {
			texts := make([]string, len(items))
			for i, m := range items {
				texts[i] = m.text
			}
			vectors, err := emb.Embed(ctx, texts)
			if err != nil {
				stopped.Store(true)
			}
			results <- result{items, vectors, err}
		}(items)
		if len(items) < o.BatchSize {
			break
		}
	}
	for ; pending > 0; pending-- {
		handle(<-results)
	}
	if failure != nil {
		return 0, failure
	}
	return cov.Missing, nil
}

// validateChunks is the final check of every saved chunk against its
// document: same model and hash, canonical boundaries, identical excerpt
// and title, no blank documents.
func validateChunks(ctx context.Context, q querier) error {
	var invalid int64
	err := q.QueryRow(ctx, `SELECT count(*) FROM semantic_chunks c LEFT JOIN source_documents d
      USING(kind,source_id,part,lang) WHERE d.hash IS NULL OR c.model<>$1 OR c.hash<>d.hash
      OR d.hash<>encode(sha256(convert_to(d.text,'UTF8')),'hex')
      OR c.chunk_start%780<>0 OR c.chunk_end<>least(c.chunk_start+900,length(d.text))
      OR (c.chunk_start<>0 AND c.chunk_start+120>=length(d.text))
      OR c.text<>substring(d.text FROM c.chunk_start+1 FOR c.chunk_end-c.chunk_start)
      OR c.title<>d.title OR btrim(d.text,$2)=''`, release.Model, GoWhitespace).Scan(&invalid)
	if err != nil {
		return err
	}
	if invalid > 0 {
		return fmt.Errorf("%d chunks failed canonical validation", invalid)
	}
	return nil
}

func ensureSearchIndexes(ctx context.Context, q querier) (*release.TextSearch, error) {
	if _, err := q.Exec(ctx, resource("search.sql")); err != nil {
		return nil, err
	}
	var version string
	if err := q.QueryRow(ctx, "SELECT extversion FROM pg_extension WHERE extname='pg_trgm'").Scan(&version); err != nil {
		return nil, err
	}
	if version != "1.6" {
		return nil, errors.New("search indexes require pg_trgm 1.6")
	}
	return &release.TextSearch{Extension: "pg_trgm", Version: version, Indexes: append([]string{}, release.SearchIndexes...)}, nil
}

// releaseTables are the tables a dump holds.
func releaseTables() []string {
	return append(publicTables(), "source_documents", "semantic_chunks", "semantic_documents", "lebleg_release")
}

// dumpDatabase writes a custom-format pg_dump of the release tables and the
// vector and pg_trgm extensions to destination, which must not exist. The
// file appears only once the dump is complete and synced.
func dumpDatabase(ctx context.Context, dsn, destination, pgDump string) error {
	if _, err := os.Lstat(destination); err == nil {
		return errors.New("refusing to overwrite existing dump")
	}
	env, _, err := clientEnv(dsn)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), "*.partial")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	args := []string{"--format=custom", "--no-owner", "--no-acl", "--no-comments", "--extension=vector", "--extension=pg_trgm"}
	for _, t := range releaseTables() {
		args = append(args, "--table=public."+t)
	}
	cmd := exec.CommandContext(ctx, pgDump, args...)
	cmd.Env = env
	cmd.Stdout = tmp
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_dump failed: %w: %s", err, firstLine(stderr.String()))
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	// A hard link is atomic and refuses an existing destination.
	return os.Link(tmp.Name(), destination)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// exportRelease dumps the database and writes manifest.json beside it, only
// after the dump is complete.
func exportRelease(ctx context.Context, dsn, out string, m release.Manifest, pgDump string) error {
	dump := filepath.Join(out, "corpus.dump")
	if err := dumpDatabase(ctx, dsn, dump, pgDump); err != nil {
		return err
	}
	f, err := os.Open(dump)
	if err != nil {
		return err
	}
	h := sha256.New()
	size, err := io.Copy(h, f)
	f.Close()
	if err != nil {
		return err
	}
	m.Dump = &release.Artifact{Path: "corpus.dump", SizeBytes: size, SHA256: hex.EncodeToString(h.Sum(nil))}
	b, err := release.Encode(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "manifest.json"), b, 0o644)
}

// BuildOptions configure a release build.
type BuildOptions struct {
	DSN        string    // the empty target database (PostgreSQL 18.6 with pgvector 0.8.2)
	SourceDSN  string    // a database loaded from data/ by data/load.sh, read only
	Calculated string    // data/article_calculated_current.jsonl; empty for none
	ReuseDSN   string    // an earlier release database to reuse vectors from; empty for none
	Repo       string    // the source repository, recorded in the manifest
	Revision   string    // the source commit, 40 hex
	ReleaseID  string    // the new release id
	AsOf       time.Time // the date texts must be in force on
	OutputDir  string    // where corpus.dump, manifest.json and coverage.json go
	Embedder   Embedder  // nil: never embed; missing chunks leave the build incomplete
	Fill       FillOptions
	NoExport   bool   // finalize the database without writing a dump
	Resume     bool   // continue this exact staging build after an incomplete run
	PGDump     string // the pg_dump executable
	License    string // dataset licence
	Log        io.Writer
}

// ErrIncomplete is a build that stopped with chunks still missing vectors.
// The staging database keeps what was done; run again with Resume.
var ErrIncomplete = errors.New("build incomplete: chunks are still missing vectors (staging kept; resume to continue)")

// Attribution is the credit every release carries.
const Attribution = "Texts: Centre for Research and Studies in Legal Informatics, Lebanese University. Dataset structure: Taktek, LLC."

func writeCoverage(out string, c Coverage) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "coverage.json"), append(b, '\n'), 0o644)
}

// Build makes a release database and, unless NoExport, its dump and
// manifest. It returns ErrIncomplete when vectors are still missing.
func Build(ctx context.Context, o BuildOptions) error {
	if o.Log == nil {
		o.Log = io.Discard
	}
	if !release.ValidRevision(o.Revision) || !release.ValidReleaseID(o.ReleaseID) || o.DSN == "" {
		return errors.New("database URL, immutable 40-hex revision and safe release ID required")
	}
	if o.PGDump == "" {
		o.PGDump = "pg_dump"
	}
	if err := os.MkdirAll(o.OutputDir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"corpus.dump", "manifest.json"} {
		if _, err := os.Lstat(filepath.Join(o.OutputDir, name)); err == nil {
			return errors.New("release artifacts already exist")
		}
	}
	asOf := o.AsOf.Format("2006-01-02")
	conn, err := pgx.Connect(ctx, o.DSN)
	if err != nil {
		return fmt.Errorf("connecting to the target database: %w", err)
	}
	defer conn.Close(context.Background())
	if !o.Resume {
		if err := assertEmpty(ctx, conn); err != nil {
			return err
		}
	}
	var serverVersion string
	if err := conn.QueryRow(ctx, "SHOW server_version").Scan(&serverVersion); err != nil {
		return err
	}
	if strings.Fields(serverVersion)[0] != "18.6" {
		return errors.New("build requires PostgreSQL 18.6")
	}
	if o.Resume {
		var finalized bool
		if err := conn.QueryRow(ctx, "SELECT to_regclass('public.lebleg_release') IS NOT NULL").Scan(&finalized); err != nil {
			return err
		}
		if finalized {
			return resumeFinalized(ctx, conn, o, asOf)
		}
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var tables map[string]int64
	var documents int64
	if o.Resume {
		var id, rev string
		var rawTables []byte
		var stateDate *string
		err := tx.QueryRow(ctx, "SELECT release_id,revision,tables,to_jsonb(s)->>'as_of_date' FROM lebleg_build_state s").Scan(&id, &rev, &rawTables, &stateDate)
		if err != nil || id != o.ReleaseID || rev != o.Revision {
			return errors.New("staging release identity mismatch")
		}
		if stateDate != nil && *stateDate != asOf {
			return errors.New("staging as_of_date mismatch")
		}
		if err := json.Unmarshal(rawTables, &tables); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM source_documents").Scan(&documents); err != nil {
			return err
		}
	} else {
		if o.SourceDSN == "" {
			return errors.New("a source database loaded from data/ is required")
		}
		source, err := pgx.Connect(ctx, o.SourceDSN+readOnly(o.SourceDSN))
		if err != nil {
			return fmt.Errorf("connecting to the source database: %w", err)
		}
		defer source.Close(context.Background())
		fmt.Fprintln(o.Log, "importing the public tables")
		if tables, err = importSource(ctx, tx, source, o.Calculated, o.AsOf); err != nil {
			return err
		}
		fmt.Fprintln(o.Log, "deriving the canonical documents")
		if documents, err = deriveDocuments(ctx, tx, o.AsOf); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, resource("semantic.sql")); err != nil {
			return err
		}
		rawTables, _ := json.Marshal(tables)
		if _, err := tx.Exec(ctx, "CREATE TABLE lebleg_build_state(release_id text,revision text,tables jsonb,as_of_date date)"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO lebleg_build_state VALUES($1,$2,$3::jsonb,$4::date)", o.ReleaseID, o.Revision, string(rawTables), asOf); err != nil {
			return err
		}
	}
	var vector string
	if err := tx.QueryRow(ctx, "SELECT extversion FROM pg_extension WHERE extname='vector'").Scan(&vector); err != nil || vector != "0.8.2" {
		return errors.New("build requires pgvector 0.8.2")
	}
	if o.ReuseDSN != "" {
		reuse, err := pgx.Connect(ctx, o.ReuseDSN+readOnly(o.ReuseDSN))
		if err != nil {
			return fmt.Errorf("connecting to the reuse database: %w", err)
		}
		n, err := reuseVectors(ctx, tx, reuse)
		reuse.Close(context.Background())
		if err != nil {
			return err
		}
		fmt.Fprintf(o.Log, "reused %d saved vectors\n", n)
	}
	report, err := coverage(ctx, tx)
	if err != nil {
		return err
	}
	if err := writeCoverage(o.OutputDir, report); err != nil {
		return err
	}
	line, _ := json.Marshal(report)
	fmt.Fprintln(o.Log, string(line))
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if report.Missing > 0 && o.Embedder != nil {
		if _, err := fillMissing(ctx, conn, o.Embedder, o.Fill); err != nil {
			return err
		}
		if report, err = coverage(ctx, conn); err != nil {
			return err
		}
	}
	if report.Missing > 0 {
		// The staging database keeps the useful work. No release row, no dump.
		return ErrIncomplete
	}
	m := release.Manifest{
		FormatVersion: 1,
		ReleaseID:     o.ReleaseID,
		GeneratedAt:   time.Now().UTC().Format("2006-01-02T15:04:05.000000Z"),
		AsOfDate:      asOf,
		Source:        release.Source{RepoID: o.Repo, Revision: o.Revision},
		Postgres:      release.Postgres{Version: "18.6", VectorVersion: "0.8.2"},
		Embedding: release.Embedding{Model: release.Model, Dimensions: release.Dimensions, TaskType: release.TaskType,
			ChunkSize: release.ChunkSize, ChunkOverlap: release.ChunkOverlap, Storage: release.Storage},
		Counts:       release.Counts{Tables: tables, SourceDocuments: documents, SemanticChunks: report.Indexed},
		Dataset:      release.Dataset{License: o.License, Attribution: Attribution},
		BuildVersion: release.Version(),
	}
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if err := validateChunks(ctx, tx); err != nil {
			return err
		}
		if err := writeCoverage(o.OutputDir, report); err != nil {
			return err
		}
		ts, err := ensureSearchIndexes(ctx, tx)
		if err != nil {
			return err
		}
		m.TextSearch = ts
		if _, err := tx.Exec(ctx, "DROP TABLE lebleg_build_state"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO semantic_documents SELECT $1,kind,source_id,part,lang,hash,$2 FROM source_documents",
			release.Model, "public-release:"+o.ReleaseID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "CREATE INDEX semantic_chunks_embedding ON semantic_chunks USING hnsw(embedding halfvec_cosine_ops)"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "CREATE TABLE lebleg_release(release_id text PRIMARY KEY,manifest jsonb NOT NULL)"); err != nil {
			return err
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO lebleg_release VALUES($1,$2::jsonb)", o.ReleaseID, string(raw)); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "ANALYZE")
		return err
	})
	if err != nil {
		return err
	}
	if o.NoExport {
		return nil
	}
	fmt.Fprintln(o.Log, "dumping the release")
	return exportRelease(ctx, o.DSN, o.OutputDir, m, o.PGDump)
}

// resumeFinalized exports a release whose database was finalized by an
// earlier run that failed while dumping.
func resumeFinalized(ctx context.Context, conn *pgx.Conn, o BuildOptions, asOf string) error {
	var raw []byte
	err := conn.QueryRow(ctx, "SELECT manifest FROM lebleg_release WHERE release_id=$1", o.ReleaseID).Scan(&raw)
	var m release.Manifest
	if err != nil || json.Unmarshal(raw, &m) != nil || m.Source != (release.Source{RepoID: o.Repo, Revision: o.Revision}) || m.AsOfDate != asOf {
		return errors.New("finalized release identity mismatch")
	}
	for table, want := range map[string]int64{"source_documents": m.Counts.SourceDocuments, "semantic_chunks": m.Counts.SemanticChunks} {
		var n int64
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != want {
			return errors.New("finalized release count mismatch")
		}
	}
	if err := validateChunks(ctx, conn); err != nil {
		return err
	}
	if o.NoExport {
		return nil
	}
	return exportRelease(ctx, o.DSN, o.OutputDir, m, o.PGDump)
}

// readOnly is the setting appended to a DSN so the session can only read.
func readOnly(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return sep + "options=-c%20default_transaction_read_only%3Don"
	}
	return " options='-c default_transaction_read_only=on'"
}
