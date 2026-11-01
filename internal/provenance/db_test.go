package provenance_test

// The database tests run the guard end to end against a throwaway Postgres
// in docker: LEBLEG_DOCKER_TEST=1 go test -p 1 -run DB ./internal/provenance/
// (on a machine with Docker). Each starts postgres:16-alpine as lebleg-test-<sha>, works
// only in databases named lebleg_test_<sha>..., and removes the container
// after. A database name that is not lebleg_test_* is refused before any
// statement, so lebleg and lebleg_next can never be touched.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/archive"
	"github.com/taktekhq/lebanese-legislation/internal/build"
	"github.com/taktekhq/lebanese-legislation/internal/provenance"
)

var testDBName = regexp.MustCompile(`^lebleg_test_[0-9a-z_]+$`)

type pg struct {
	name string // container
	sha  string
	port string
}

var (
	pgOnce sync.Once
	pgInst *pg
	pgErr  error
)

// postgres starts the container once per test binary; TestMain removes it.
func postgres(t *testing.T) *pg {
	t.Helper()
	if os.Getenv("LEBLEG_DOCKER_TEST") != "1" {
		t.Skip("LEBLEG_DOCKER_TEST=1 runs the database tests (docker, postgres:16-alpine)")
	}
	pgOnce.Do(func() {
		b := make([]byte, 5)
		rand.Read(b)
		p := &pg{sha: hex.EncodeToString(b)}
		p.name = "lebleg-test-" + p.sha
		out, err := exec.Command("docker", "run", "-d", "--rm", "--name", p.name,
			"-e", "POSTGRES_HOST_AUTH_METHOD=trust", "-p", "127.0.0.1::5432", "postgres:16-alpine").CombinedOutput()
		if err != nil {
			pgErr = fmt.Errorf("docker run: %v: %s", err, out)
			return
		}
		pgInst = p
		for i := 0; i < 60; i++ {
			out, err = exec.Command("docker", "port", p.name, "5432/tcp").Output()
			if err == nil && len(out) > 0 {
				line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
				p.port = line[strings.LastIndex(line, ":")+1:]
				if exec.Command("docker", "exec", p.name, "pg_isready", "-U", "postgres", "-h", "127.0.0.1").Run() == nil {
					return
				}
			}
			time.Sleep(time.Second)
		}
		pgErr = fmt.Errorf("postgres in %s did not come up", p.name)
	})
	if pgErr != nil {
		t.Fatal(pgErr)
	}
	return pgInst
}

func TestMain(m *testing.M) {
	code := m.Run()
	if pgInst != nil {
		exec.Command("docker", "rm", "-f", pgInst.name).Run()
	}
	os.Exit(code)
}

// database creates a fresh database lebleg_test_<sha>_<suffix> and returns
// its DSN. Any other name is refused.
func (p *pg) database(t *testing.T, suffix string) string {
	t.Helper()
	name := "lebleg_test_" + p.sha + "_" + suffix
	if !testDBName.MatchString(name) {
		t.Fatalf("refusing database %q: tests only touch lebleg_test_*", name)
	}
	admin := p.dsn("postgres")
	conn, err := pgx.Connect(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	return p.dsn(name)
}

func (p *pg) dsn(name string) string {
	return fmt.Sprintf("host=127.0.0.1 port=%s user=postgres dbname=%s sslmode=disable", p.port, name)
}

func TestDBRefusesOtherNames(t *testing.T) {
	for _, n := range []string{"lebleg", "lebleg_next", "postgres", "lebleg_testx", "lebleg_test_"} {
		if testDBName.MatchString(n) {
			t.Errorf("%q would be accepted", n)
		}
	}
}

func exec1(t *testing.T, dsn string, sqls ...string) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	for _, s := range sqls {
		if _, err := conn.Exec(context.Background(), s); err != nil {
			t.Fatalf("%v\n%.200s", err, s)
		}
	}
}

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// lebleg builds this checkout's binary once.
func lebleg(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lebleg-provenance-test")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "lebleg")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/lebleg")
		cmd.Dir = "../.."
		if out, err := cmd.CombinedOutput(); err != nil {
			binErr = fmt.Errorf("go build: %v: %s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
}

// run runs a lebleg subcommand ("export", "provenance check") with the
// fixture's sources, and returns its exit code, stdout and stderr.
func run(t *testing.T, sub string, args ...string) (int, string, string) {
	t.Helper()
	abs := func(p string) string { a, _ := filepath.Abs(p); return a }
	full := strings.Fields(sub)
	full = append(full, "-archive", abs("testdata/archive"), "-scans", abs("testdata/bundle"),
		"-edits", abs("../../db/approved_edits.tsv")+","+abs("../../db/article_corrections.tsv")+","+abs("../../db/centre_errors.tsv"),
		"-known", abs("../../db/provenance_known.tsv"))
	full = append(full, args...)
	cmd := exec.Command(lebleg(t), full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, stdout.String(), stderr.String()
}

func schema(t *testing.T, dsn string) {
	exec1(t, dsn, db.Schema, db.AmendmentSchema, db.GraphSchema)
}

// The build's writers store each text's source with it, and the guard
// passes what they wrote: the fixture archive (four laws, two amending the
// other two, one with an earlier wording an amendment quotes) and one
// scanned text, built from scratch.
func TestDBBuildWritesSources(t *testing.T) {
	p := postgres(t)
	dsn := p.database(t, "build")
	_, err := build.Run(context.Background(), build.Options{Archive: archive.Archive{Dir: "testdata/archive"}, DSN: dsn,
		Scans: "testdata/bundle", Citations: "testdata/none.jsonl", RulingCitations: "testdata/none.jsonl", Log: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	got := map[string]int{}
	rows, _ := conn.Query(context.Background(), `SELECT 'a ' || coalesce(source_kind, '-') || ' ' || coalesce(source_id, '-'), count(*)
		FROM legislation_article GROUP BY 1
		UNION ALL SELECT 'v ' || text_source || ' ' || coalesce(source_kind, '-') || ' ' || coalesce(source_id, '-'), count(*)
		FROM article_version GROUP BY 1`)
	for rows.Next() {
		var k string
		var n int
		rows.Scan(&k, &n)
		got[k] = n
	}
	rows.Close()
	for _, k := range []string{"a ul lawtext/8225", "a ul lawtext/9307", "a ul lawtext/326577", "a ul lawtext/327389",
		"a saderlex 197135", "v centre ul lawtext/8225", "v amendment amendment 9307/1", "v amendment amendment 327389/1"} {
		if got[k] == 0 {
			t.Errorf("no %q rows; got %v", k, got)
		}
	}
	for k := range got {
		if strings.Contains(k, " -") && !strings.HasPrefix(k, "v none") {
			t.Errorf("rows without a source: %q", k)
		}
	}
	if os.Getenv("LEBLEG_WRITE_FIXTURE") == "1" {
		// Regenerate testdata/fixture.sql, the dump the next test loads, from
		// what the writers stored.
		out, err := exec.Command("docker", "exec", p.name, "pg_dump", "-U", "postgres", "--data-only", "--column-inserts",
			"--no-owner", "--no-privileges", "-d", "lebleg_test_"+p.sha+"_build").Output()
		if err != nil {
			t.Fatal(err)
		}
		// psql's own lines (\restrict) are not SQL.
		out = regexp.MustCompile(`(?m)^\\.*\n`).ReplaceAll(out, nil)
		if err := os.WriteFile("testdata/fixture.sql", out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g, err := provenance.Open(guardPaths())
	if err != nil {
		t.Fatal(err)
	}
	vs, st, err := provenance.CheckDBStats(context.Background(), dsn, g.Sources, g.Edits, g.Known)
	if err != nil || len(vs) != 0 || st.Inferred || st.Texts == 0 {
		t.Fatalf("the build's own texts: %v %+v %v", vs, st, err)
	}
	// A database built before the source columns: the guard infers each
	// source from ids the way the writers store it, and still passes.
	exec1(t, dsn, `ALTER TABLE legislation_article DROP COLUMN source_kind CASCADE, DROP COLUMN source_id CASCADE`,
		`ALTER TABLE article_version DROP COLUMN source_kind CASCADE, DROP COLUMN source_id CASCADE`)
	vs, st, err = provenance.CheckDBStats(context.Background(), dsn, g.Sources, g.Edits, g.Known)
	if err != nil || len(vs) != 0 || !st.Inferred {
		t.Fatalf("inferred: %v %+v %v", vs, st, err)
	}
	exec1(t, dsn, `UPDATE article_version SET text_ar = text_ar || ' وتلغى' WHERE text_source = 'amendment'`)
	if vs, _ := provenance.CheckDB(context.Background(), dsn, g.Sources, g.Edits, g.Known); len(vs) == 0 {
		t.Fatal("inferred: a rewritten amendment wording passed")
	}
}

func guardPaths() provenance.Paths {
	return provenance.Paths{Archive: "testdata/archive", Bundle: "testdata/bundle",
		Edits: []string{"../../db/approved_edits.tsv", "../../db/article_corrections.tsv", "../../db/centre_errors.tsv"},
		Known: "../../db/provenance_known.tsv"}
}

// The fixture dump loads, passes, exports with no provenance column; one
// article rewritten as a model would, by an UPDATE that keeps its source,
// blocks export and snapshot with exit 4, names the article, and writes no
// file.
func TestDBModelRewriteBlocksExportAndSnapshot(t *testing.T) {
	p := postgres(t)
	dsn := p.database(t, "fixture")
	schema(t, dsn)
	dump, err := os.ReadFile("testdata/fixture.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec1(t, dsn, string(dump))

	if code, _, stderr := run(t, "provenance check", "-db", dsn); code != 0 {
		t.Fatalf("clean fixture: exit %d\n%s", code, stderr)
	}
	tmp := t.TempDir()
	clean := filepath.Join(tmp, "clean")
	if code, _, stderr := run(t, "export", "-db", dsn, "-out", clean); code != 0 {
		t.Fatalf("clean export: exit %d\n%s", code, stderr)
	}
	for _, f := range []string{"data/legislation_article/COLUMNS", "data/article_version/COLUMNS", "schema.sql"} {
		b, err := os.ReadFile(filepath.Join(clean, f))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("source_kind")) || (f != "schema.sql" && bytes.Contains(b, []byte("source_id"))) {
			t.Errorf("export's %s has a provenance column", f)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(clean, "data/legislation_scan/COLUMNS")); !bytes.Contains(b, []byte("source_id")) {
		t.Errorf("export dropped legislation_scan's own source_id: %q", b)
	}
	snap := filepath.Join(tmp, "snapshot")
	if code, _, stderr := run(t, "snapshot", "-db", dsn, "-out", snap); code != 0 {
		t.Fatalf("clean snapshot: exit %d\n%s", code, stderr)
	}
	files, _ := filepath.Glob(filepath.Join(snap, "*.sql"))
	if len(files) < 2 {
		t.Fatalf("clean snapshot wrote %v", files)
	}
	for _, f := range files {
		if b, _ := os.ReadFile(f); bytes.Contains(b, []byte("source_kind")) || bytes.Contains(b, []byte("lawtext/")) {
			t.Errorf("snapshot %s carries provenance", filepath.Base(f))
		}
	}
	hfColumns(t, dsn)

	// The model's rewrite: one word of law 8225's article 1, source kept.
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	if err := conn.QueryRow(context.Background(), `SELECT text_ar FROM legislation_article WHERE law_id = 8225 AND number = '1'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	w := strings.Fields(text)
	w[len(w)/2] = "ويستثنى"
	if _, err := conn.Exec(context.Background(), `UPDATE legislation_article SET text_ar = $1 WHERE law_id = 8225 AND number = '1'`,
		strings.Join(w, " ")); err != nil {
		t.Fatal(err)
	}
	conn.Close(context.Background())

	for _, c := range []struct{ name, out string }{{"export", filepath.Join(tmp, "blocked-export")}, {"snapshot", filepath.Join(tmp, "blocked-snapshot")}} {
		code, _, stderr := run(t, c.name, "-db", dsn, "-out", c.out)
		if code != 4 {
			t.Errorf("%s: exit %d, want 4\n%s", c.name, code, stderr)
		}
		if !strings.Contains(stderr, "wording guard: 1 articles without source") || !strings.Contains(stderr, "law 8225 article 1: not in source ul/lawtext/8225") {
			t.Errorf("%s: stderr does not name the article:\n%s", c.name, stderr)
		}
		if _, err := os.Stat(c.out); !os.IsNotExist(err) {
			t.Errorf("%s wrote %s after the guard blocked", c.name, c.out)
		}
	}
	code, stdout, _ := run(t, "provenance check", "-db", dsn, "--report")
	if code != 4 || !strings.Contains(stdout, "law 8225\tarticle 1\tarticle\tnot in source ul/lawtext/8225\tsha256 ") {
		t.Errorf("check --report: exit %d\n%s", code, stdout)
	}
}

// An amendment row's wordings (text_ar, and old_text_ar, the wording it
// quotes as replaced) are law text the export and hf.sh publish: each must
// be a span of the amending article, the row's own by_law_id/by_article.
func TestDBAmendmentTextsChecked(t *testing.T) {
	p := postgres(t)
	dsn := p.database(t, "amendment")
	schema(t, dsn)
	dump, err := os.ReadFile("testdata/fixture.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec1(t, dsn, string(dump))
	g, err := provenance.Open(guardPaths())
	if err != nil {
		t.Fatal(err)
	}
	var n int
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if err := conn.QueryRow(context.Background(), `SELECT count(*) FROM amendment WHERE coalesce(text_ar, '') <> ''`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("the fixture has no amendment wording (%d, %v)", n, err)
	}
	vs, st, err := provenance.CheckDBStats(context.Background(), dsn, g.Sources, g.Edits, g.Known)
	if err != nil || len(vs) != 0 || st.Amendments < n {
		t.Fatalf("clean: %v %+v %v (want %d amendment wordings checked)", vs, st, err, n)
	}
	for _, col := range []string{"text_ar", "old_text_ar"} {
		var id int64
		if err := conn.QueryRow(context.Background(), `SELECT min(id) FROM amendment WHERE coalesce(`+col+`, '') <> ''`).Scan(&id); err != nil {
			if col == "old_text_ar" {
				exec1(t, dsn, `UPDATE amendment SET old_text_ar = text_ar WHERE id = (SELECT min(id) FROM amendment WHERE text_ar <> '')`)
				conn.QueryRow(context.Background(), `SELECT min(id) FROM amendment WHERE coalesce(old_text_ar, '') <> ''`).Scan(&id)
			} else {
				t.Fatal(err)
			}
		}
		exec1(t, dsn, `UPDATE amendment SET `+col+` = `+col+` || ' ويستثنى القضاة' WHERE id = `+strconv.FormatInt(id, 10))
		vs, _, err := provenance.CheckDBStats(context.Background(), dsn, g.Sources, g.Edits, g.Known)
		want := fmt.Sprintf("amendment %d %s", id, col)
		if err != nil || len(vs) != 1 || vs[0].Row != want || !strings.HasPrefix(vs[0].Reason, "not in source amendment/") {
			t.Fatalf("%s rewritten: %+v %v, want one violation on %q", col, vs, err, want)
		}
		exec1(t, dsn, `UPDATE amendment SET `+col+` = left(`+col+`, -length(' ويستثنى القضاة')) WHERE id = `+strconv.FormatInt(id, 10))
	}
}

// A database with no law text at all (half built, or emptied) is not a
// pass: there is nothing to vouch for, and export writes nothing.
func TestDBEmptyIsNotAPass(t *testing.T) {
	p := postgres(t)
	dsn := p.database(t, "empty")
	schema(t, dsn)
	code, _, stderr := run(t, "provenance check", "-db", dsn)
	if code == 0 || !strings.Contains(stderr, "no law text to check") {
		t.Errorf("check on an empty database: exit %d\n%s", code, stderr)
	}
	out := filepath.Join(t.TempDir(), "export")
	if code, _, stderr := run(t, "export", "-db", dsn, "-out", out); code == 0 {
		t.Errorf("export of an empty database: exit 0\n%s", stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("export wrote its folder from an empty database")
	}
}

// hfColumns runs hf.sh's own column query for the tables it publishes.
func hfColumns(t *testing.T, dsn string) {
	t.Helper()
	sh, err := os.ReadFile("../../deploy/hf.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)cols=\$\(psql "\$DB" -XAtq -c "(SELECT string_agg.*?)"\)`).FindSubmatch(sh)
	if m == nil {
		t.Fatal("hf.sh's column query not found")
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	for _, table := range []string{"legislation_article", "article_version", "legislation_scan"} {
		var cols *string
		q := strings.ReplaceAll(string(m[1]), "$t", table)
		if err := conn.QueryRow(context.Background(), q).Scan(&cols); err != nil || cols == nil {
			t.Fatalf("%s: %v", table, err)
		}
		has := strings.Contains(*cols, "source_kind") || strings.Contains(*cols, "source_id")
		if table == "legislation_scan" {
			if !strings.Contains(*cols, "source_id") {
				t.Errorf("hf.sh drops legislation_scan's own source_id: %s", *cols)
			}
		} else if has {
			t.Errorf("hf.sh publishes a provenance column of %s: %s", table, *cols)
		}
	}
}
