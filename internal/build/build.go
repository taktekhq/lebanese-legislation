// Package build loads the parsed archive into Postgres, from scratch.
//
// Documents are parsed in chunks, in parallel, then written in id order, so
// memory stays flat (a chunk at a time) and two builds from the same archive
// produce the same rows. Indexes are created after the data is in.
package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/archive"
	"github.com/taktekhq/lebanese-legislation/internal/modelout"
	"github.com/taktekhq/lebanese-legislation/internal/page"
)

// Options configure a build.
type Options struct {
	Archive archive.Archive
	DSN     string
	Workers int
	Chunk   int
	Log     func(format string, args ...any)
	// Only reruns one step on the database as it is ("amendments"), for
	// working on it without a full rebuild.
	Only string
	// Citations is the model's citations file (default db/model/citations.jsonl).
	Citations string
	// RulingCitations is the model's reading of rulings: the texts each applies,
	// cites or interprets: a comma-separated list of files
	// (default modelout.DefaultRulingCitations, the full texts and the summaries).
	RulingCitations string
	// Scans is a lebgazette sader-bundle folder: texts the Centre has not
	// published yet, read from the Gazette's own pages (scans.go). Empty: none.
	Scans string
	// RulingFiles is the folder `lebleg ruling-files` fills: each ruling's
	// scanned judgment (PDF, left where it is) and `lebleg ruling-read`'s
	// machine reading of it, loaded into ruling_file and ruling_page
	// (internal/modelout). Empty: none.
	RulingFiles string
}

type builder struct {
	Options
	ctx  context.Context
	conn *pgx.Conn

	kinds, courts  map[string]bool
	laws           map[int]bool // loaded texts, for foreign keys
	sections       map[int]bool
	articleIDs     map[int]bool
	gazette        map[int][2]string // law id -> printed gazette number, date
	rep            *Report
	prior          []stateRow            // page_state of the database being replaced (indexing.go)
	priorEdges     modelout.PriorEdgeIDs // graph_edge ids of the database being replaced, kept for the same edges (modelsteps.go)
	priorEdgesRead bool
}

// Run rebuilds the database and returns what it found.
func Run(ctx context.Context, o Options) (*Report, error) {
	if o.Workers <= 0 {
		o.Workers = 4
	}
	if o.Chunk <= 0 {
		o.Chunk = 400
	}
	conn, err := pgx.Connect(ctx, o.DSN)
	if err != nil {
		return nil, err
	}
	defer conn.Close(context.Background())
	b := &builder{Options: o, ctx: ctx, conn: conn,
		kinds: map[string]bool{}, courts: map[string]bool{}, laws: map[int]bool{},
		sections: map[int]bool{}, articleIDs: map[int]bool{}, gazette: map[int][2]string{},
		rep: newReport()}
	b.rep.Only = o.Only
	start := time.Now()
	steps := []struct {
		name string
		fn   func() error
	}{
		{"prior state", b.priorState},
		{"schema", b.schema},
		{"vocabularies", b.vocabularies},
		{"legislation", b.legislation},
		{"scans", b.scans},
		{"dates", b.dates},
		{"centre errors", b.centreErrors},
		{"council rulings", b.councilRulings},
		{"english titles", b.titlesEn},
		{"rulings", b.rulings},
		{"ruling files", b.rulingFiles},
		{"opinions", b.opinions},
		{"agreements", b.agreements},
		{"doctrine", b.doctrine},
		{"research", b.research},
		{"gazette", b.gazetteIssues},
		{"scan gazette", b.scanGazette},
		{"codes", b.codes},
		{"amendments", b.amendments},
		{"graph", b.graph},
		{"indexes", b.indexes},
		{"indexing", b.indexing},
	}
	if o.Only == "amendments" {
		steps = steps[:0]
		steps = append(steps, struct {
			name string
			fn   func() error
		}{"amendments", func() error {
			if _, err := b.conn.Exec(b.ctx, db.AmendmentSchema); err != nil {
				return err
			}
			return b.amendments()
		}})
	} else if o.Only == "ruling-files" {
		steps = steps[:0]
		steps = append(steps, struct {
			name string
			fn   func() error
		}{"ruling files", b.rulingFiles})
	} else if o.Only == "indexing" {
		steps = steps[:0]
		steps = append(steps, struct {
			name string
			fn   func() error
		}{"indexing", b.indexing})
	} else if o.Only == "scan-gazette" {
		steps = steps[:0]
		steps = append(steps, struct {
			name string
			fn   func() error
		}{"scan gazette", b.scanGazette})
	} else if o.Only == "graph" {
		steps = steps[:0]
		steps = append(steps, struct {
			name string
			fn   func() error
		}{"graph", b.graph})
	} else if o.Only != "" {
		return nil, fmt.Errorf("build: no step %q to run alone", o.Only)
	}
	for _, s := range steps {
		t := time.Now()
		if err := s.fn(); err != nil {
			return b.rep, fmt.Errorf("%s: %w", s.name, err)
		}
		o.Log("%-13s %6.1fs", s.name, time.Since(t).Seconds())
	}
	b.rep.Took = time.Since(start)
	return b.rep, b.rep.count(ctx, conn)
}

func (b *builder) schema() error {
	_, err := b.conn.Exec(b.ctx, "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;")
	if err == nil {
		_, err = b.conn.Exec(b.ctx, db.Schema)
	}
	if err == nil {
		_, err = b.conn.Exec(b.ctx, db.AmendmentSchema)
	}
	return err
}

func (b *builder) indexes() error {
	if _, err := b.conn.Exec(b.ctx, db.Indexes); err != nil {
		return err
	}
	_, err := b.conn.Exec(b.ctx, "ANALYZE")
	return err
}

// vocabularies loads the Centre's kinds and courts, with their ids from its
// search forms and the names in db/names.tsv.
func (b *builder) vocabularies() error {
	names := map[string][2]string{} // list|arabic -> en, fr
	for _, line := range strings.Split(db.Names, "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 4 && !strings.HasPrefix(line, "#") && f[0] != "list" {
			names[f[0]+"|"+f[1]] = [2]string{f[2], f[3]}
		}
	}
	load := func(list, file, selectID, table string, into map[string]bool) error {
		opts, err := options(filepath.Join(b.Archive.Dir, "pages", file), selectID)
		if err != nil {
			return err
		}
		for _, o := range opts {
			if err := b.vocab(table, list, o.name, &o.id, names, into); err != nil {
				return err
			}
		}
		for k := range names {
			if n := strings.TrimPrefix(k, list+"|"); n != k && !into[n] {
				if err := b.vocab(table, list, n, nil, names, into); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := load("kind", "LegisltaionSearch.aspx_language=ar.html", "drp_clasification", "legislation_kind", b.kinds); err != nil {
		return err
	}
	return load("court", "AdvancedRulingSearch.aspx_language=ar.html", "drpcourt", "court", b.courts)
}

func (b *builder) vocab(table, list, name string, id *int, names map[string][2]string, into map[string]bool) error {
	if into[name] {
		return nil
	}
	n := names[list+"|"+name]
	_, err := b.conn.Exec(b.ctx, "INSERT INTO "+table+" (name_ar, centre_id, name_en, name_fr) VALUES ($1, $2, $3, $4)",
		name, id, nullable(n[0]), nullable(n[1]))
	into[name] = true
	if n[0] == "" && id == nil {
		b.rep.note("%s %q is printed on pages but is not in the Centre's list", list, name)
	}
	return err
}

// ensure adds a kind or court seen on a page but missing from the lists.
func (b *builder) ensure(table, list, name string, into map[string]bool) error {
	if name == "" || into[name] {
		return nil
	}
	return b.vocab(table, list, name, nil, map[string][2]string{}, into)
}

type option struct {
	id   int
	name string
}

func options(file, selectID string) ([]option, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	d, err := page.Parse(raw)
	if err != nil {
		return nil, err
	}
	var out []option
	page.Walk(d.Root, func(n *html.Node) bool {
		if page.Is(n, atom.Select) && strings.Contains(page.Attr(n, "id"), selectID) {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if page.Is(c, atom.Option) {
					var id int
					if _, err := fmt.Sscan(page.Attr(c, "value"), &id); err == nil && id > 0 {
						out = append(out, option{id, page.Inline(c)})
					}
				}
			}
			return false
		}
		return true
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no options in #%s", file, selectID)
	}
	return out, nil
}

// chunked parses entries in parallel, a chunk at a time, and hands each
// chunk's results to write in the original (id) order.
func chunked[T any](b *builder, entries []archive.Entry, parseOne func(archive.Entry) (T, error), write func([]T) error) error {
	for lo := 0; lo < len(entries); lo += b.Chunk {
		hi := min(lo+b.Chunk, len(entries))
		out := make([]T, hi-lo)
		errs := make([]error, hi-lo)
		var wg sync.WaitGroup
		next := make(chan int)
		for w := 0; w < b.Workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range next {
					out[i], errs[i] = parseOne(entries[lo+i])
				}
			}()
		}
		for i := range out {
			next <- i
		}
		close(next)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				return fmt.Errorf("%s %d: %w", entries[lo+i].Type, entries[lo+i].ID, err)
			}
		}
		if err := write(out); err != nil {
			return err
		}
	}
	return nil
}

// rows collects COPY rows per table and writes them in a fixed table order,
// parents before children, so foreign keys hold.
type rows struct {
	order []string
	cols  map[string][]string
	data  map[string][][]any
}

func newRows() *rows { return &rows{cols: map[string][]string{}, data: map[string][][]any{}} }

func (r *rows) add(table string, cols []string, vals ...any) {
	if _, ok := r.cols[table]; !ok {
		r.order = append(r.order, table)
		r.cols[table] = cols
	}
	r.data[table] = append(r.data[table], vals)
}

func (b *builder) flush(r *rows, first ...string) error {
	order := append([]string{}, first...)
	for _, t := range r.order {
		if !contains(order, t) {
			order = append(order, t)
		}
	}
	for _, t := range order {
		if len(r.data[t]) == 0 {
			continue
		}
		if _, err := b.conn.CopyFrom(b.ctx, pgx.Identifier{t}, r.cols[t], pgx.CopyFromRows(r.data[t])); err != nil {
			return fmt.Errorf("copy %s: %w", t, err)
		}
	}
	return nil
}

var sourceCols = []string{"doc_type", "centre_id", "url", "archive_path", "sha256", "first_seen", "last_changed"}

func source(r *rows, e archive.Entry) {
	// The Centre is HTTPS-only since Oct 2026; pages fetched before then
	// were recorded with http://, which now answers 404.
	url := strings.Replace(e.URL, "http://legallaw.ul.edu.lb/", "https://legallaw.ul.edu.lb/", 1)
	r.add("source_page", sourceCols, e.Type, e.ID, url, archive.Rel(e.Type, e.ID), e.SHA256, e.FirstSeen, e.LastChanged)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
