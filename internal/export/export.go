// Package export writes the database out as git-friendly text.
//
// Each table becomes tables/<table>/<shard>.tsv in PostgreSQL's COPY text
// format: one row per line (tabs and line breaks inside values are escaped),
// sorted by key, split by key range so no file nears GitHub's 100 MB limit.
// An unchanged row is an unchanged line, so git stores only what changed.
// Generated columns (search vectors) are left out; Postgres recomputes them
// on load. The same database always produces byte-identical files.
//
// The wording guard (internal/provenance) runs first: when any law text in
// the database is not a span of its archived source, nothing is written and
// Run returns provenance.ErrBlocked (exit 4). The provenance columns it
// reads (source_kind, source_id) are internal and never exported.
package export

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/provenance"
)

const limit = 90 << 20 // GitHub refuses files over 100 MB

// Table describes how one table is written.
type Table struct {
	Name    string
	Columns []string // written, in order (generated columns left out)
	Order   []string // sort key
	Key     string   // integer column that picks the shard
	keyIdx  int
}

// Shard plan, kept in the repository so shard boundaries never move.
type plan map[string]int64 // table -> key width per shard

// Run exports the database at dsn into the repository at dir, once the
// wording guard g passes it.
func Run(ctx context.Context, dsn, dir string, g provenance.Guard, log func(string, ...any)) (map[string]int64, error) {
	if err := g.Gate(ctx, dsn); err != nil {
		return nil, err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer conn.Close(context.Background())
	tables, err := describe(ctx, conn)
	if err != nil {
		return nil, err
	}
	p := plan{}
	planPath := filepath.Join(dir, "shards.json")
	if b, err := os.ReadFile(planPath); err == nil {
		if err := json.Unmarshal(b, &p); err != nil {
			return nil, fmt.Errorf("shards.json: %w", err)
		}
	}
	counts := map[string]int64{}
	written := map[string]bool{}
	var order []string
	for _, t := range tables {
		if _, ok := p[t.Name]; !ok {
			if p[t.Name], err = width(ctx, conn, t); err != nil {
				return nil, err
			}
		}
		n, files, err := t.write(ctx, conn, dir, p[t.Name])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.Name, err)
		}
		counts[t.Name] = n
		for _, f := range files {
			written[f] = true
		}
		order = append(order, t.Name)
		log("%-22s %10d rows  %3d files", t.Name, n, len(files))
		cols := strings.Join(t.Columns, ",") + "\n"
		if err := os.WriteFile(filepath.Join(dir, "tables", t.Name, "COLUMNS"), []byte(cols), 0o644); err != nil {
			return nil, err
		}
		written[filepath.Join(dir, "tables", t.Name, "COLUMNS")] = true
	}
	// Shards whose key range emptied out, and tables that no longer exist.
	filepath.Walk(filepath.Join(dir, "tables"), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && !written[path] {
			os.Remove(path)
		}
		return nil
	})
	files := map[string]string{
		"schema.sql":  provenance.StripInternal(db.Schema + "\n" + db.AmendmentSchema + "\n" + db.GraphSchema),
		"indexes.sql": db.Indexes,
		"TABLES":      strings.Join(order, "\n") + "\n",
	}
	b, _ := json.MarshalIndent(p, "", " ")
	files["shards.json"] = string(b) + "\n"
	b, _ = json.MarshalIndent(counts, "", " ")
	files["stats.json"] = string(b) + "\n"
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return nil, err
		}
	}
	return counts, nil
}

// describe lists the public tables in load order (parents before children),
// with their non-generated columns and a stable sort key.
func describe(ctx context.Context, conn *pgx.Conn) ([]Table, error) {
	rows, err := conn.Query(ctx, `
		SELECT c.relname,
		  array(SELECT a.attname FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attnum > 0
		        AND NOT a.attisdropped AND a.attgenerated = '' ORDER BY a.attnum),
		  coalesce((SELECT array(SELECT a.attname FROM unnest(k.conkey) WITH ORDINALITY u(n, i)
		        JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = u.n ORDER BY u.i)
		     FROM pg_constraint k WHERE k.conrelid = c.oid AND k.contype IN ('p', 'u')
		     ORDER BY k.contype = 'p' DESC, k.conname LIMIT 1), '{}'),
		  array(SELECT DISTINCT r.relname FROM pg_constraint f JOIN pg_class r ON r.oid = f.confrelid
		        WHERE f.conrelid = c.oid AND f.contype = 'f' AND f.confrelid <> c.oid)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r' ORDER BY c.relname`)
	if err != nil {
		return nil, err
	}
	type info struct {
		cols, key, deps []string
	}
	all := map[string]info{}
	var names []string
	for rows.Next() {
		var name string
		var i info
		if err := rows.Scan(&name, &i.cols, &i.key, &i.deps); err != nil {
			return nil, err
		}
		i.cols = Public(name, i.cols)
		all[name] = i
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Integer columns, to choose the shard key.
	ints := map[string]bool{}
	rows, err = conn.Query(ctx, `SELECT table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND data_type IN ('integer', 'bigint', 'smallint')`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		ints[s] = true
	}
	// Topological order, alphabetical among equals, so it never varies.
	var order []string
	done := map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		if done[n] {
			return
		}
		done[n] = true
		deps := append([]string{}, all[n].deps...)
		sort.Strings(deps)
		for _, d := range deps {
			visit(d)
		}
		order = append(order, n)
	}
	sort.Strings(names)
	for _, n := range names {
		visit(n)
	}
	var out []Table
	for _, n := range order {
		i := all[n]
		t := Table{Name: n, Columns: i.cols, Order: i.key}
		if len(t.Order) == 0 {
			t.Order = i.cols
		}
		for _, c := range t.Order {
			if ints[n+"."+c] {
				t.Key = c
				break
			}
		}
		t.keyIdx = -1
		for j, c := range t.Columns {
			if c == t.Key {
				t.keyIdx = j
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// Public is a table's columns without the internal provenance ones.
func Public(table string, cols []string) []string {
	out := cols[:0:0]
	for _, c := range cols {
		if !provenance.Internal(table, c) {
			out = append(out, c)
		}
	}
	return out
}

// keySpace sizes a table's shards by another table's ids and a shard count
// of its own. It is for tables that fill in over weeks: the judgments'
// readings arrive from the low ids up, and sizing them by the ids read so far
// would put the rest into a shard the first export had made far too wide.
// A page of a judgment is ~2.5 KB and a ruling has 5 to 20 of them, so
// ruling_page gets twice the usual 40 shards: the densest of 4,000 rulings
// would pass 80 MB, too near the 90 MB limit that fails the export.
type keySpace struct {
	table, column string
	shards        int64
}

var keySpaces = map[string]keySpace{
	"ruling_file": {"ruling", "id", 40},
	"ruling_page": {"ruling", "id", 80},
}

// width picks the key range per shard. Shard boundaries are fixed once
// chosen (they live in shards.json), so they are sized for the table's key
// space, not today's row count: about 40 shards across the ids a large table
// uses, which keeps each file small even as full texts keep arriving. Tables
// with a small key space stay in one file.
func width(ctx context.Context, conn *pgx.Conn, t Table) (int64, error) {
	if t.Key == "" {
		return 0, nil
	}
	src, shards := keySpace{t.Name, t.Key, 40}, int64(40)
	if k, ok := keySpaces[t.Name]; ok {
		src, shards = k, k.shards
	}
	var hi int64
	err := conn.QueryRow(ctx, fmt.Sprintf(`SELECT coalesce(max(%s), 0) FROM %s`,
		pgx.Identifier{src.column}.Sanitize(), pgx.Identifier{src.table}.Sanitize())).Scan(&hi)
	if err != nil || hi < 40_000 {
		return 0, err
	}
	w := (hi + shards) / shards
	return (w + 999) / 1000 * 1000, nil
}

func ident(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = pgx.Identifier{c}.Sanitize()
	}
	return strings.Join(q, ", ")
}

// write streams the table out, routing each line to its key range's file.
func (t Table) write(ctx context.Context, conn *pgx.Conn, dir string, w int64) (int64, []string, error) {
	base := filepath.Join(dir, "tables", t.Name)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return 0, nil, err
	}
	sh := &sharder{t: t, width: w, base: base, files: map[int64]*shard{}}
	sql := fmt.Sprintf("COPY (SELECT %s FROM %s ORDER BY %s) TO STDOUT",
		ident(t.Columns), pgx.Identifier{t.Name}.Sanitize(), ident(t.Order))
	if _, err := conn.PgConn().CopyTo(ctx, sh, sql); err != nil {
		sh.close()
		return 0, nil, err
	}
	if err := sh.flushPartial(); err != nil {
		return 0, nil, err
	}
	return sh.rows, sh.close(), sh.err
}

type shard struct {
	f    *os.File
	w    *bufio.Writer
	size int64
	path string
}

type sharder struct {
	t       Table
	width   int64
	base    string
	files   map[int64]*shard
	partial []byte
	rows    int64
	err     error
}

func (s *sharder) Write(p []byte) (int, error) {
	data := append(s.partial, p...)
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		if err := s.line(data[:i+1]); err != nil {
			return 0, err
		}
		data = data[i+1:]
	}
	s.partial = append(s.partial[:0], data...)
	return len(p), nil
}

func (s *sharder) flushPartial() error {
	if len(s.partial) > 0 {
		return s.line(append(s.partial, '\n'))
	}
	return nil
}

func (s *sharder) line(l []byte) error {
	var n int64
	if s.width > 0 && s.t.keyIdx >= 0 {
		f := field(l, s.t.keyIdx)
		if v, err := strconv.ParseInt(string(f), 10, 64); err == nil {
			n = v / s.width
		}
	}
	sh := s.files[n]
	if sh == nil {
		path := filepath.Join(s.base, fmt.Sprintf("%06d.tsv", n))
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		sh = &shard{f: f, w: bufio.NewWriterSize(f, 1<<20), path: path}
		s.files[n] = sh
	}
	sh.size += int64(len(l))
	if sh.size > limit {
		return fmt.Errorf("%s passes %d MB; lower its width in shards.json", sh.path, limit>>20)
	}
	s.rows++
	_, err := sh.w.Write(l)
	return err
}

func (s *sharder) close() []string {
	var paths []string
	for _, sh := range s.files {
		if err := sh.w.Flush(); err != nil && s.err == nil {
			s.err = err
		}
		sh.f.Close()
		paths = append(paths, sh.path)
	}
	sort.Strings(paths)
	return paths
}

// field returns the i-th tab-separated field of a COPY text line.
func field(l []byte, i int) []byte {
	for ; i > 0; i-- {
		j := bytes.IndexByte(l, '\t')
		if j < 0 {
			return nil
		}
		l = l[j+1:]
	}
	if j := bytes.IndexAny(l, "\t\n"); j >= 0 {
		return l[:j]
	}
	return l
}
