package releasedb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/release"
	"github.com/taktekhq/lebanese-legislation/internal/semantic"
)

// The database tests need a throwaway PostgreSQL 18.6 with pgvector 0.8.2
// (docker build -f db/Dockerfile --target postgres) and an administrator
// DSN in LEBLEG_RELEASE_TEST_DSN. Each test makes its own databases and
// drops them. Dump and restore tests also need pg_dump and pg_restore 18.6
// (LEBLEG_TEST_PGDUMP, LEBLEG_TEST_PGRESTORE).

func adminDSN(t *testing.T) string {
	dsn := os.Getenv("LEBLEG_RELEASE_TEST_DSN")
	if dsn == "" {
		t.Skip("set LEBLEG_RELEASE_TEST_DSN to a throwaway PostgreSQL 18.6 with pgvector")
	}
	return dsn
}

func withDB(dsn, name string) string {
	if strings.HasPrefix(dsn, "postgres") {
		i := strings.LastIndex(dsn, "/")
		rest := ""
		if q := strings.Index(dsn[i:], "?"); q >= 0 {
			rest = dsn[i+q:]
		}
		return dsn[:i+1] + name + rest
	}
	return dsn + " dbname=" + name
}

// newDB creates an empty database and returns its DSN.
func newDB(t *testing.T) string {
	t.Helper()
	admin := adminDSN(t)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "lt_" + hex.EncodeToString(b)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return withDB(admin, name)
}

func run(t *testing.T, dsn string, statements ...string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	for _, s := range statements {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
}

func scalar[T any](t *testing.T, dsn, query string, args ...any) T {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var v T
	if err := conn.QueryRow(ctx, query, args...).Scan(&v); err != nil {
		t.Fatalf("%v: %s", err, query)
	}
	return v
}

// longText is three chunks long, with an emoji and Arabic, so offsets in
// code points and bytes differ.
var longText = strings.Repeat("نص المادة 😀 ", 160)

// sourceDB is a small database shaped like one loaded from data/: the
// public tables with a few rows.
func sourceDB(t *testing.T) string {
	dsn := newDB(t)
	schema := resource("public_schema.sql")
	i := strings.Index(schema, "ALTER TABLE")
	run(t, dsn, schema[:i])
	var keys, foreign []string
	for _, st := range strings.Split(schema[i:], ";") {
		if strings.TrimSpace(st) == "" {
			continue
		}
		if strings.Contains(st, "FOREIGN KEY") {
			foreign = append(foreign, st)
		} else {
			keys = append(keys, st)
		}
	}
	run(t, dsn, keys...)
	run(t, dsn, foreign...)
	run(t, dsn,
		`INSERT INTO legislation_kind(name_ar,name_en) VALUES('قانون','Law'),('مرسوم','Decree')`,
		`INSERT INTO court(name_ar,name_en) VALUES('محكمة','Court')`,
		`INSERT INTO legislation(id,kind,number,issued_on,title,status,preamble,title_en) VALUES
		  (1,'قانون','10','2019-01-01','نص أصلي','in_force','مقدمة',NULL),
		  (2,'مرسوم','11','2020-03-01','نص ثان','in_force',NULL,NULL),
		  (3,'قانون','12','2999-01-01','نص مستقبلي','in_force',NULL,NULL)`,
		`INSERT INTO legislation_title_en VALUES(1,'An unofficial title','fixture')`,
		`INSERT INTO legislation_article(law_id,position,number,text_ar,text_fr) VALUES
		  (1,1,'1','`+longText+`','Texte'),(1,2,'2 مكرر','مادة ثانية',NULL),(2,1,'1','   ',NULL)`,
		`INSERT INTO article_in_force(law_id,number,position,state,text_ar,pending) VALUES
		  (1,'1',1,'in_force','`+longText+`',false),(1,'2 مكرر',2,'in_force','مادة ثانية',false),
		  (2,'1',1,'in_force','   ',false)`,
		`INSERT INTO ruling(id,court,number,year,decided_on,title,text_ar) VALUES(5,'محكمة','7',2001,'2001-05-05','قرار','نص القرار')`,
		`INSERT INTO ruling_page(ruling_id,n,text_ar,model,low_confidence) VALUES(5,1,'صفحة أولى','reader',true)`,
	)
	return dsn
}

// fakeEmbedder returns a fixed unit vector per text and counts its calls.
type fakeEmbedder struct {
	calls, texts atomic.Int64
	fail         func(call int64) error
	mu           sync.Mutex
	seen         []string
}

func (f *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	n := f.calls.Add(1)
	if f.fail != nil {
		if err := f.fail(n); err != nil {
			return nil, err
		}
	}
	f.texts.Add(int64(len(texts)))
	f.mu.Lock()
	f.seen = append(f.seen, texts...)
	f.mu.Unlock()
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, release.Dimensions)
		v[i%release.Dimensions] = 1
		out[i] = v
	}
	return out, nil
}

func buildOptions(t *testing.T, source, target, out string) BuildOptions {
	return BuildOptions{DSN: target, SourceDSN: source, Repo: release.DefaultRepo,
		Revision: strings.Repeat("a", 40), ReleaseID: "test-1", AsOf: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		OutputDir: out, NoExport: true, License: "test licence",
		Fill: FillOptions{Cap: 1000, Workers: 2, BatchSize: 2}}
}

func TestBuildDerivesDocumentsAndEmbedsMissingChunks(t *testing.T) {
	ctx := context.Background()
	source, target := sourceDB(t), newDB(t)
	emb := &fakeEmbedder{}
	o := buildOptions(t, source, target, t.TempDir())
	o.Embedder = emb
	if err := Build(ctx, o); err != nil {
		t.Fatal(err)
	}
	// The future law, the blank article and nothing else is left out.
	if n := scalar[int64](t, target, "SELECT count(*) FROM source_documents WHERE source_id=3 OR btrim(text)=''"); n != 0 {
		t.Fatalf("ineligible documents: %d", n)
	}
	url := scalar[string](t, target, "SELECT source_url FROM source_documents WHERE kind='article' AND part='2 مكرر'")
	if url != "https://lebaneselegislation.com/law/1/article/2-%D9%85%D9%83%D8%B1%D8%B1/" {
		t.Fatalf("article url %s", url)
	}
	if u := scalar[string](t, target, "SELECT source_url FROM source_documents WHERE kind='law' AND source_id=2 LIMIT 1"); u != "https://lebaneselegislation.com/decree/2/" {
		t.Fatalf("decree url %s", u)
	}
	if en := scalar[string](t, target, "SELECT metadata->>'english_unofficial' FROM source_documents WHERE kind='law' AND lang='en'"); en != "true" {
		t.Fatalf("english metadata %s", en)
	}
	if d := scalar[string](t, target, "SELECT metadata->>'date' FROM source_documents WHERE kind='ruling' AND part=''"); d != "2001-05-05" {
		t.Fatalf("ruling date %s", d)
	}
	// Every canonical chunk has a vector, cut as internal/semantic cuts it.
	chunks := semantic.Chunks(semantic.Document{Text: longText})
	if n := scalar[int64](t, target, "SELECT count(*) FROM semantic_chunks WHERE kind='article' AND source_id=1 AND part='1' AND lang='ar'"); n != int64(len(chunks)) {
		t.Fatalf("long article has %d chunks, want %d", n, len(chunks))
	}
	for _, c := range chunks {
		got := scalar[string](t, target, "SELECT text FROM semantic_chunks WHERE kind='article' AND source_id=1 AND part='1' AND lang='ar' AND chunk_start=$1 AND chunk_end=$2", c.Start, c.End)
		if got != c.Text {
			t.Fatalf("chunk %d-%d differs", c.Start, c.End)
		}
	}
	docs := scalar[int64](t, target, "SELECT count(*) FROM semantic_chunks")
	if emb.texts.Load() != docs {
		t.Fatalf("embedded %d texts for %d chunks", emb.texts.Load(), docs)
	}
	raw := scalar[[]byte](t, target, "SELECT manifest FROM lebleg_release")
	if !strings.Contains(string(raw), `"build_version"`) || !strings.Contains(string(raw), `"text_search"`) {
		t.Fatalf("manifest %s", raw)
	}
}

func TestBuildReusesOnlyCanonicalVectors(t *testing.T) {
	ctx := context.Background()
	source, first := sourceDB(t), newDB(t)
	o := buildOptions(t, source, first, t.TempDir())
	o.Embedder = &fakeEmbedder{}
	if err := Build(ctx, o); err != nil {
		t.Fatal(err)
	}
	// The second release: one article changes, one title changes, and the
	// earlier release holds a vector at a non-canonical offset.
	run(t, source, `UPDATE article_in_force SET text_ar='مادة معدلة' WHERE number='2 مكرر'`,
		`UPDATE legislation SET title='عنوان جديد' WHERE id=1`)
	run(t, first, `INSERT INTO semantic_chunks(model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding)
		SELECT model,kind,source_id,part,lang,title,substring(text from 2),hash,1,chunk_end,embedding FROM semantic_chunks
		WHERE kind='article' AND part='1' AND lang='ar' AND chunk_start=0`)
	second := newDB(t)
	emb := &fakeEmbedder{}
	o = buildOptions(t, source, second, t.TempDir())
	o.ReleaseID, o.ReuseDSN, o.Embedder = "test-2", first, emb
	if err := Build(ctx, o); err != nil {
		t.Fatal(err)
	}
	for _, text := range emb.seen {
		if strings.HasPrefix(text, "نص المادة") {
			t.Fatalf("an unchanged chunk was embedded again")
		}
	}
	if n := scalar[int64](t, second, "SELECT count(*) FROM semantic_chunks WHERE chunk_start % 780 <> 0"); n != 0 {
		t.Fatal("a non-canonical vector was reused")
	}
	if n := scalar[int64](t, second, "SELECT count(*) FROM semantic_chunks WHERE source_id=1 AND kind IN ('article','law') AND lang='ar' AND title<>'عنوان جديد'"); n != 0 {
		t.Fatal("a reused vector kept the old title")
	}
	if !contains(emb.seen, "مادة معدلة") {
		t.Fatal("the changed article was not embedded")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestCapRefusesBeforeAnyRequest(t *testing.T) {
	source, target := sourceDB(t), newDB(t)
	emb := &fakeEmbedder{}
	o := buildOptions(t, source, target, t.TempDir())
	o.Embedder, o.Fill.Cap = emb, 1
	err := Build(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "exceeds explicit cap") {
		t.Fatalf("got %v", err)
	}
	if emb.calls.Load() != 0 {
		t.Fatal("a request was made past the cap")
	}
}

func TestFailureKeepsPaidBatchesAndResumes(t *testing.T) {
	ctx := context.Background()
	source, target := sourceDB(t), newDB(t)
	boom := errors.New("provider down")
	emb := &fakeEmbedder{fail: func(n int64) error {
		if n == 3 {
			return boom
		}
		return nil
	}}
	out := t.TempDir()
	o := buildOptions(t, source, target, out)
	o.Embedder, o.Fill.Workers = emb, 1
	if err := Build(ctx, o); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	saved := scalar[int64](t, target, "SELECT count(*) FROM semantic_chunks")
	if saved != 4 { // two batches of two before the failure
		t.Fatalf("saved %d chunks before the failure, want 4", saved)
	}
	// No release row yet; a resume embeds only the rest.
	if n := scalar[bool](t, target, "SELECT to_regclass('public.lebleg_release') IS NULL"); !n {
		t.Fatal("an incomplete build was finalized")
	}
	again := &fakeEmbedder{}
	o.Embedder, o.Resume = again, true
	if err := Build(ctx, o); err != nil {
		t.Fatal(err)
	}
	total := scalar[int64](t, target, "SELECT count(*) FROM semantic_chunks")
	if again.texts.Load() != total-saved {
		t.Fatalf("resume embedded %d, want %d", again.texts.Load(), total-saved)
	}
}

func TestBuildWithoutEmbedderIsIncomplete(t *testing.T) {
	source, target := sourceDB(t), newDB(t)
	o := buildOptions(t, source, target, t.TempDir())
	if err := Build(context.Background(), o); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(o.OutputDir, "coverage.json")); err != nil {
		t.Fatal("no coverage report")
	}
}

func TestBuildRefusesOccupiedTarget(t *testing.T) {
	source, target := sourceDB(t), newDB(t)
	run(t, target, "CREATE FUNCTION public.stray() RETURNS int LANGUAGE sql AS 'SELECT 1'")
	o := buildOptions(t, source, target, t.TempDir())
	if err := Build(context.Background(), o); err == nil || !strings.Contains(err.Error(), "occupied") {
		t.Fatalf("got %v", err)
	}
}

func TestSourceTypeMustMatchThePublicContract(t *testing.T) {
	source, target := sourceDB(t), newDB(t)
	run(t, source, "ALTER TABLE ruling ALTER COLUMN year TYPE integer")
	o := buildOptions(t, source, target, t.TempDir())
	o.Embedder = &fakeEmbedder{}
	if err := Build(context.Background(), o); err == nil || !strings.Contains(err.Error(), "ruling.year") {
		t.Fatalf("got %v", err)
	}
}

func TestLegislationTakesItsEnglishTitleFromItsTable(t *testing.T) {
	source, target := sourceDB(t), newDB(t)
	run(t, source, "ALTER TABLE legislation DROP COLUMN title_en")
	o := buildOptions(t, source, target, t.TempDir())
	o.Embedder = &fakeEmbedder{}
	if err := Build(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if v := scalar[string](t, target, "SELECT title_en FROM legislation WHERE id=1"); v != "An unofficial title" {
		t.Fatalf("title_en %q", v)
	}
}

func clients(t *testing.T) (string, string) {
	dump, restore := os.Getenv("LEBLEG_TEST_PGDUMP"), os.Getenv("LEBLEG_TEST_PGRESTORE")
	if dump == "" || restore == "" {
		t.Skip("set LEBLEG_TEST_PGDUMP and LEBLEG_TEST_PGRESTORE to pg_dump and pg_restore 18.6")
	}
	return dump, restore
}

const testWebSQL = "CREATE TABLE web_schema(ok boolean); CREATE FUNCTION web_marker() RETURNS int LANGUAGE sql AS 'SELECT 1';"

// built makes a release directory once per test.
func built(t *testing.T) (string, *release.Release) {
	pgDump, _ := clients(t)
	source, target := sourceDB(t), newDB(t)
	out := t.TempDir()
	o := buildOptions(t, source, target, out)
	o.Embedder, o.NoExport, o.PGDump = &fakeEmbedder{}, false, pgDump
	if err := Build(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	r, err := release.Load(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	return out, r
}

func readerDB(t *testing.T) string {
	dsn := newDB(t)
	run(t, adminDSN(t), `DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='lt_reader') THEN
		CREATE ROLE lt_reader LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION; END IF; END $$`)
	return dsn
}

func TestDumpRestoresIdempotentlyWithReadOnlyReader(t *testing.T) {
	ctx := context.Background()
	out, r := built(t)
	_, pgRestore := clients(t)
	if r.Counts.SemanticChunks == 0 || r.Counts.Tables["legislation"] != 3 {
		t.Fatalf("manifest counts %+v", r.Counts)
	}
	target := readerDB(t)
	o := RestoreOptions{DSN: target, Reader: "lt_reader", PGRestore: pgRestore, WebSQL: testWebSQL}
	dump := filepath.Join(out, r.Dump.Path)
	got, err := Restore(ctx, r, dump, o)
	if err != nil || got != "restored" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err = Restore(ctx, r, dump, o); err != nil || got != "already restored" {
		t.Fatalf("second restore: %q %v", got, err)
	}
	if !scalar[bool](t, target, "SELECT has_table_privilege('lt_reader','legislation','SELECT') AND NOT has_table_privilege('lt_reader','legislation','INSERT')") {
		t.Fatal("reader is not read-only")
	}
	if scalar[int](t, target, "SELECT web_marker()") != 1 {
		t.Fatal("web.sql did not run")
	}
	if err := CheckReceipt(ctx, target, filepath.Join(out, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	// Another release into the same database is refused.
	other := *r
	other.Raw = r.External()
	other.Raw["release_id"] = "test-other"
	other.ReleaseID = "test-other"
	if _, err := Restore(ctx, &other, dump, o); err == nil || !strings.Contains(err.Error(), "occupied") {
		t.Fatalf("got %v", err)
	}
}

func TestRestoreRefusesMissingReaderAndBadChecksum(t *testing.T) {
	ctx := context.Background()
	out, r := built(t)
	_, pgRestore := clients(t)
	target := newDB(t)
	dump := filepath.Join(out, r.Dump.Path)
	_, err := Restore(ctx, r, dump, RestoreOptions{DSN: target, Reader: "no_such_role", PGRestore: pgRestore, WebSQL: testWebSQL})
	if err == nil || !strings.Contains(err.Error(), "reader role") {
		t.Fatalf("got %v", err)
	}
	if n := scalar[int64](t, target, "SELECT count(*) FROM pg_tables WHERE schemaname='public'"); n != 0 {
		t.Fatal("a refused restore wrote tables")
	}
	bad := *r
	d := *r.Dump
	d.SHA256 = strings.Repeat("0", 64)
	bad.Dump = &d
	if _, err := Restore(ctx, &bad, dump, RestoreOptions{DSN: target, PGRestore: pgRestore, WebSQL: testWebSQL}); !errors.Is(err, release.ErrChecksum) {
		t.Fatalf("got %v", err)
	}
}

func TestFailedChecksLeaveNoReceiptAndResume(t *testing.T) {
	ctx := context.Background()
	out, r := built(t)
	_, pgRestore := clients(t)
	target := newDB(t)
	dump := filepath.Join(out, r.Dump.Path)
	// web.sql failing after the import: no receipt, and a retry with a good
	// web.sql finishes without importing again.
	_, err := Restore(ctx, r, dump, RestoreOptions{DSN: target, PGRestore: pgRestore, WebSQL: "SELECT no_such_function()"})
	if err == nil {
		t.Fatal("broken web.sql passed")
	}
	if scalar[bool](t, target, "SELECT to_regclass('public.restore_receipt') IS NOT NULL") {
		t.Fatal("receipt written after a failure")
	}
	got, err := Restore(ctx, r, dump, RestoreOptions{DSN: target, PGRestore: "/bin/false-but-unused", WebSQL: testWebSQL})
	if err == nil {
		t.Fatalf("an unusable pg_restore was accepted: %q", got)
	}
	got, err = Restore(ctx, r, dump, RestoreOptions{DSN: target, PGRestore: pgRestore, WebSQL: testWebSQL})
	if err != nil || got != "restored" {
		t.Fatalf("resume: %q %v", got, err)
	}
}

func TestResumeExportsAFinalizedRelease(t *testing.T) {
	pgDump, _ := clients(t)
	source, target := sourceDB(t), newDB(t)
	out := t.TempDir()
	o := buildOptions(t, source, target, out)
	o.Embedder, o.PGDump, o.NoExport = &fakeEmbedder{}, "/nonexistent/pg_dump", false
	if err := Build(context.Background(), o); err == nil {
		t.Fatal("a missing pg_dump passed")
	}
	if _, err := os.Stat(filepath.Join(out, "manifest.json")); err == nil {
		t.Fatal("manifest written without a dump")
	}
	o.PGDump, o.Resume, o.Embedder = pgDump, true, nil
	if err := Build(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if _, err := release.Load(filepath.Join(out, "manifest.json")); err != nil {
		t.Fatal(err)
	}
}
