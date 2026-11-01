// Package releasedb does the database work of a public release: restoring a
// published dump with its checks (restore.go), and building a new release
// database from the data with reused and new embeddings (build.go).
package releasedb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/taktekhq/lebanese-legislation/internal/release"
)

// ErrRestore is wrapped by every restore failure. Messages never carry a
// connection string.
var ErrRestore = errors.New("restore failed")

func restoreErr(msg string) error { return fmt.Errorf("%w: %s", ErrRestore, msg) }

// querier is what both a connection and a transaction offer.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const releaseTargetState = `SELECT tablename FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')
 UNION SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
   WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%'
   AND c.relkind IN ('v','m','S','f')
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid='pg_class'::regclass AND d.objid=c.oid AND d.deptype='e')
 UNION SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
   WHERE n.nspname NOT IN ('pg_catalog','information_schema')
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e')
 UNION SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
   WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_%'
   AND t.typrelid=0 AND t.typelem=0
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid='pg_type'::regclass AND d.objid=t.oid AND d.deptype='e')
 UNION SELECT nspname FROM pg_namespace WHERE nspname NOT IN ('public','pg_catalog','information_schema')
   AND nspname NOT LIKE 'pg_%'`

func strings1(ctx context.Context, q querier, sql string, args ...any) ([]string, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// targetState says what the target database holds: "empty", "ready" (this
// release, already restored and checked), "attempt_empty" (an earlier
// attempt of this release that imported nothing), or "resume" (an earlier
// attempt of this release whose import finished). Anything else is refused.
func targetState(ctx context.Context, q querier, r *release.Release) (string, error) {
	names, err := strings1(ctx, q, releaseTargetState)
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "empty", nil
	}
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	if set["restore_receipt"] {
		var raw []byte
		err := q.QueryRow(ctx, "SELECT manifest FROM public.restore_receipt WHERE singleton=true").Scan(&raw)
		if err == nil {
			var receipt map[string]any
			if json.Unmarshal(raw, &receipt) == nil {
				delete(receipt, "artifact_revision")
				if release.Equal(receipt, r.External()) {
					return "ready", nil
				}
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	if set["restore_attempt"] {
		var sum string
		var owner, relowner, current uint32
		err := q.QueryRow(ctx, `SELECT a.manifest_sha256,a.owner_oid,c.relowner,current_user::regrole::oid
			FROM public.restore_attempt a JOIN pg_class c ON c.oid='public.restore_attempt'::regclass
			WHERE a.singleton`).Scan(&sum, &owner, &relowner, &current)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if err == nil && sum == r.Checksum() && owner == relowner && relowner == current {
			if len(set) == 1 {
				return "attempt_empty", nil
			}
			expected := map[string]bool{"restore_attempt": true, "source_documents": true, "semantic_chunks": true,
				"semantic_documents": true, "lebleg_release": true}
			for t := range r.Counts.Tables {
				expected[t] = true
			}
			subset := true
			for n := range set {
				if !expected[n] {
					subset = false
				}
			}
			if subset {
				return "resume", nil
			}
		}
	}
	return "", restoreErr("target database is occupied or belongs to another release; use a new database")
}

func checkReader(ctx context.Context, q querier, reader string) error {
	if reader == "" {
		return nil
	}
	var flags [5]bool
	err := q.QueryRow(ctx, "SELECT rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls FROM pg_roles WHERE rolname=$1", reader).
		Scan(&flags[0], &flags[1], &flags[2], &flags[3], &flags[4])
	if errors.Is(err, pgx.ErrNoRows) {
		return restoreErr("a restricted existing reader role is required")
	}
	if err != nil {
		return err
	}
	for _, f := range flags {
		if f {
			return restoreErr("a restricted existing reader role is required")
		}
	}
	return nil
}

func beginAttempt(ctx context.Context, conn *pgx.Conn, r *release.Release) error {
	manifest, err := json.Marshal(r.Raw)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TABLE restore_attempt(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
			manifest_sha256 text NOT NULL,manifest jsonb NOT NULL,owner_oid oid NOT NULL,
			started_at timestamptz NOT NULL DEFAULT now())`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "INSERT INTO restore_attempt(singleton,manifest_sha256,manifest,owner_oid) VALUES(true,$1,$2::jsonb,current_user::regrole::oid)",
			r.Checksum(), string(manifest))
		return err
	})
}

func checkSearchIndexes(ctx context.Context, q querier, spec *release.TextSearch) error {
	var version string
	err := q.QueryRow(ctx, "SELECT extversion FROM pg_extension WHERE extname=$1", spec.Extension).Scan(&version)
	if err != nil || version != spec.Version {
		return restoreErr("restored text search extension mismatch")
	}
	rows, err := q.Query(ctx, `SELECT c.relname,i.indisvalid,i.indisready,a.amname,pg_get_indexdef(c.oid)
		FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace
		JOIN pg_am a ON a.oid=c.relam WHERE i.indrelid='public.source_documents'::regclass AND n.nspname='public'`)
	if err != nil {
		return err
	}
	type index struct {
		valid, ready bool
		method, def  string
	}
	indexes := map[string]index{}
	for rows.Next() {
		var name string
		var ix index
		if err := rows.Scan(&name, &ix.valid, &ix.ready, &ix.method, &ix.def); err != nil {
			rows.Close()
			return err
		}
		indexes[name] = ix
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range spec.Indexes {
		ix, ok := indexes[name]
		if !ok || !ix.valid || !ix.ready {
			return restoreErr("restored text search index is missing or invalid")
		}
		def := strings.ToLower(ix.def)
		var valid bool
		if strings.HasSuffix(name, "_trgm") {
			column := "text"
			if name == "source_documents_title_trgm" {
				column = "title"
			}
			valid = ix.method == "gin" && strings.Contains(def, "("+column+" gin_trgm_ops)")
		} else {
			keys := []string{"issued_on", "date"}
			if strings.HasSuffix(name, "_identifier") {
				keys = []string{"identifier", "number"}
			}
			valid = ix.method == "btree" && strings.Contains(def, "coalesce")
			for _, k := range keys {
				valid = valid && strings.Contains(def, "'"+k+"'")
			}
		}
		if !valid {
			return restoreErr("restored text search index definition mismatch")
		}
	}
	return nil
}

// CheckDatabase verifies a restored or built release database against its
// manifest: server and pgvector versions, every row count, the release row,
// text search indexes, the vector column and index, source hashes, chunk
// boundaries and, when reader is set, that the reader can only read.
func CheckDatabase(ctx context.Context, q querier, r *release.Release, reader string) error {
	var version int
	if err := q.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		return err
	}
	if version != 180006 {
		return restoreErr("PostgreSQL 18.6 is required")
	}
	var vector string
	if err := q.QueryRow(ctx, "SELECT extversion FROM pg_extension WHERE extname='vector'").Scan(&vector); err != nil || vector != "0.8.2" {
		return restoreErr("pgvector 0.8.2 is required")
	}
	counts := map[string]int64{"source_documents": r.Counts.SourceDocuments, "semantic_chunks": r.Counts.SemanticChunks}
	for t, n := range r.Counts.Tables {
		counts[t] = n
	}
	for _, t := range sortedTables(counts) {
		var actual int64
		if err := q.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{t}.Sanitize()).Scan(&actual); err != nil {
			return restoreErr("restored table count mismatch")
		}
		if actual != counts[t] {
			return restoreErr("restored table count mismatch")
		}
	}
	rows, err := q.Query(ctx, "SELECT release_id,manifest FROM lebleg_release")
	if err != nil {
		return restoreErr("restored release identity mismatch")
	}
	type identity struct {
		id  string
		raw []byte
	}
	var ids []identity
	for rows.Next() {
		var i identity
		if err := rows.Scan(&i.id, &i.raw); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, i)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) != 1 || ids[0].id != r.ReleaseID {
		return restoreErr("restored release identity mismatch")
	}
	var internal map[string]any
	if err := json.Unmarshal(ids[0].raw, &internal); err != nil {
		return restoreErr("restored release metadata mismatch")
	}
	keys := []string{"source", "embedding", "counts"}
	if _, ok := r.Raw["text_search"]; ok {
		keys = append(keys, "text_search")
	}
	for _, k := range keys {
		if !release.Equal(internal[k], r.Raw[k]) {
			return restoreErr("restored release metadata mismatch")
		}
	}
	if r.TextSearch != nil {
		if err := checkSearchIndexes(ctx, q, r.TextSearch); err != nil {
			return err
		}
	}
	var vectorType string
	err = q.QueryRow(ctx, "SELECT format_type(atttypid,atttypmod) FROM pg_attribute WHERE attrelid='semantic_chunks'::regclass AND attname='embedding'").Scan(&vectorType)
	if err != nil || vectorType != release.Storage {
		return restoreErr("restored embedding schema mismatch")
	}
	var one int
	err = q.QueryRow(ctx, "SELECT 1 FROM pg_indexes WHERE tablename='semantic_chunks' AND indexdef LIKE '%USING hnsw%' AND indexdef LIKE '%halfvec_cosine_ops%'").Scan(&one)
	if err != nil {
		return restoreErr("restored vector index is missing")
	}
	err = q.QueryRow(ctx, `SELECT 1 FROM source_documents
		WHERE hash IS DISTINCT FROM encode(sha256(convert_to(text,'UTF8')),'hex') LIMIT 1`).Scan(&one)
	if err == nil {
		return restoreErr("restored source hash mismatch")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	err = q.QueryRow(ctx, `SELECT 1 FROM semantic_chunks c LEFT JOIN source_documents d USING(kind,source_id,part,lang)
		WHERE d.hash IS NULL OR c.hash IS DISTINCT FROM d.hash OR c.chunk_start<0
		  OR c.chunk_end<=c.chunk_start OR c.chunk_end>char_length(d.text) OR c.chunk_end-c.chunk_start>900
		  OR c.text IS DISTINCT FROM substring(d.text from c.chunk_start+1 for c.chunk_end-c.chunk_start)
		  OR c.model<>$1 LIMIT 1`, r.Embedding.Model).Scan(&one)
	if err == nil {
		return restoreErr("restored semantic source mismatch")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if reader != "" {
		if err := checkReader(ctx, q, reader); err != nil {
			return err
		}
		tables := map[string]int64{"source_documents": 0, "semantic_chunks": 0, "lebleg_release": 0}
		for t := range r.Counts.Tables {
			tables[t] = 0
		}
		for _, t := range sortedTables(tables) {
			var canRead, canWrite bool
			err := q.QueryRow(ctx, "SELECT has_table_privilege($1,$2,'SELECT'),has_table_privilege($1,$2,'INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')",
				reader, t).Scan(&canRead, &canWrite)
			if err != nil {
				return err
			}
			if !canRead || canWrite {
				return restoreErr("reader table permissions are not read-only")
			}
		}
	}
	return nil
}

func sortedTables(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// applyWebSchema adds what the Go services read and the dump lacks (web.sql)
// when the corpus tables exist.
func applyWebSchema(ctx context.Context, conn *pgx.Conn, webSQL string) error {
	var present bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.legislation_article') IS NOT NULL").Scan(&present); err != nil {
		return err
	}
	if !present {
		return nil
	}
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, webSQL)
		return err
	})
}

func finishRestore(ctx context.Context, conn *pgx.Conn, r *release.Release, reader, webSQL string) error {
	if err := applyWebSchema(ctx, conn, webSQL); err != nil {
		return err
	}
	// ANALYZE outside the grant transaction: holding its relation locks with
	// uncommitted GRANT catalog updates can deadlock with autovacuum.
	if _, err := conn.Exec(ctx, "ANALYZE"); err != nil {
		return err
	}
	manifest, err := json.Marshal(r.Raw)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if reader != "" {
			if err := checkReader(ctx, tx, reader); err != nil {
				return err
			}
			role := pgx.Identifier{reader}.Sanitize()
			if _, err := tx.Exec(ctx, "GRANT USAGE ON SCHEMA public TO "+role); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "GRANT SELECT ON ALL TABLES IN SCHEMA public TO "+role); err != nil {
				return err
			}
		}
		if err := CheckDatabase(ctx, tx, r, reader); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "CREATE TABLE restore_receipt(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),manifest jsonb NOT NULL,restored_at timestamptz NOT NULL DEFAULT now())"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO restore_receipt(singleton,manifest) VALUES(true,$1::jsonb)", string(manifest)); err != nil {
			return err
		}
		if reader != "" {
			if _, err := tx.Exec(ctx, "GRANT SELECT ON restore_receipt TO "+pgx.Identifier{reader}.Sanitize()); err != nil {
				return err
			}
		}
		return nil
	})
}

var pg186 = regexp.MustCompile(`\b18\.6\b`)

// RestoreOptions configure a restore.
type RestoreOptions struct {
	DSN       string
	Reader    string // a restricted existing role given SELECT; empty for none
	PGRestore string // the pg_restore executable, 18.6
	WebSQL    string // the website schema (web.sql) applied after the import
}

// Restore verifies dump against the release and restores it into an empty
// database (or finishes an interrupted restore of the same release). It
// never drops existing data. It returns "restored" or "already restored".
func Restore(ctx context.Context, r *release.Release, dump string, o RestoreOptions) (string, error) {
	if err := release.VerifyFile(dump, *r.Dump); err != nil {
		return "", err
	}
	if o.PGRestore == "" {
		o.PGRestore = "pg_restore"
	}
	out, err := exec.CommandContext(ctx, o.PGRestore, "--version").Output()
	if err != nil {
		return "", restoreErr("matching pg_restore executable is required")
	}
	if !pg186.Match(out) {
		return "", restoreErr("pg_restore 18.6 is required")
	}
	conn, err := pgx.Connect(ctx, o.DSN)
	if err != nil {
		return "", restoreErr("database restore or validation failed; target was not marked ready")
	}
	defer conn.Close(context.Background())
	q := conn
	// The session advisory lock covers the child pg_restore and the checks.
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(172913,1)").Scan(&locked); err != nil {
		return "", err
	}
	if !locked {
		return "", restoreErr("another restore is running")
	}
	var version int
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		return "", err
	}
	if version != 180006 {
		return "", restoreErr("PostgreSQL 18.6 is required")
	}
	if err := checkReader(ctx, q, o.Reader); err != nil {
		return "", err
	}
	state, err := targetState(ctx, q, r)
	if err != nil {
		return "", err
	}
	switch state {
	case "ready":
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error { return CheckDatabase(ctx, tx, r, o.Reader) })
		if err != nil {
			return "", err
		}
		return "already restored", nil
	case "resume":
		if err := finishRestore(ctx, conn, r, o.Reader, o.WebSQL); err != nil {
			return "", err
		}
		return "restored", nil
	case "empty":
		if err := beginAttempt(ctx, conn, r); err != nil {
			return "", err
		}
	}
	env, db, err := clientEnv(o.DSN)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dump)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, o.PGRestore, "--dbname="+db, "--no-owner", "--no-acl", "--single-transaction", "--exit-on-error", abs)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", restoreErr("transactional restore failed; target was not marked ready")
	}
	if err := finishRestore(ctx, conn, r, o.Reader, o.WebSQL); err != nil {
		return "", err
	}
	return "restored", nil
}

// ReadWebSQL reads the website schema file.
func ReadWebSQL(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("website schema: %w", err)
	}
	return string(b), nil
}
