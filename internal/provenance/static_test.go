package provenance_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/export"
	"github.com/taktekhq/lebanese-legislation/internal/provenance"
	"github.com/taktekhq/lebanese-legislation/internal/snapshot"
)

// No file in the repository writes law text but the build's writers, and
// none of those reads model output.
func TestScanWritersRepo(t *testing.T) {
	if fs := provenance.ScanWriters("../.."); len(fs) != 0 {
		for _, f := range fs {
			t.Error(f)
		}
	}
	// The writers are there and do write: the scan sees them.
	for _, w := range provenance.WriterFiles {
		if _, err := os.Stat(filepath.Join("../..", w)); err != nil {
			t.Errorf("writer file %s: %v", w, err)
		}
	}
}

// Python fixture SQL belongs under testdata, not a production writer
// exemption. Identical code outside testdata must still be flagged, and
// scanning the fixtures explicitly must retain their write findings.
func TestScanWritersPythonTestdataClassification(t *testing.T) {
	root := t.TempDir()
	fixtureDir := filepath.Join(root, "testdata")
	if err := os.Mkdir(fixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	code := []byte(`db.execute("INSERT INTO legislation(id,title,title_norm,status) VALUES(244565,'fixture','fixture','in_force')")`)
	for _, target := range []string{filepath.Join(root, "writer_test.py"), filepath.Join(fixtureDir, "fixture.py")} {
		if err := os.WriteFile(target, code, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	findings := provenance.ScanWriters(root)
	if len(findings) != 1 || findings[0].Path != "writer_test.py" || findings[0].Reason != "writes legislation outside the build's writers" {
		t.Fatalf("production Python test-named writer must remain flagged: %v", findings)
	}
	findings = provenance.ScanWriters(fixtureDir)
	if len(findings) != 1 || findings[0].Path != "fixture.py" || findings[0].Reason != "writes legislation outside the build's writers" {
		t.Fatalf("explicitly scanned fixture must retain its write finding: %v", findings)
	}
}

const fixtures = "github.com/taktekhq/lebanese-legislation/internal/provenance/testdata/writers/"

// The fixture packages, by what each must give. A package that writes law
// text is flagged for writing outside the build's writers, and again when
// it reads model output: in the writing file (modelwriter), in a helper of
// its own package (helper), or through an import that is not a decision
// package (viaimport, and viadecision while decisions is not declared one).
// A package that reads model output is flagged too when it may write law
// text by a path no write pattern sees: a text table's name passed to a
// helper (copyhelper), an UPDATE whose columns are joined on later
// (splitupdate), a table named in a constant (consttable).
// judgereader and plain write no law text; decisions writes none either;
// patternonly names model output only in a regular expression's pattern.
func TestScanWritersFixtures(t *testing.T) {
	got := map[string][]string{}
	for _, f := range provenance.ScanWriters("testdata/writers") {
		pkg, _, _ := strings.Cut(f.Path, "/")
		got[pkg] = append(got[pkg], f.Reason)
	}
	want := map[string][]string{
		"modelwriter": {"package modelwriter reads model output (modelwriter/modelwriter.go:25 names db/model/) and writes legislation_article",
			"writes legislation_article outside the build's writers"},
		"helper": {"package helper reads model output (helper/read.go:11 names db/model/) and writes article_version",
			"writes article_version outside the build's writers"},
		"viaimport": {"package viaimport reads model output (imports " + fixtures + "judgereader, which reads model output (judgereader/judgereader.go:13 names db/model/)) and writes legislation_article.text_ar",
			"writes legislation_article.text_ar outside the build's writers"},
		"viadecision": {"package viadecision reads model output (imports " + fixtures + "decisions, which reads model output (decisions/decisions.go:18 names db/model/)) and writes legislation_article",
			"writes legislation_article outside the build's writers"},
		"copyhelper": {"package copyhelper reads model output (copyhelper/copyhelper.go:20 names db/model/) and may write law text: legislation_article passed to copyRows"},
		"splitupdate": {"package splitupdate reads model output (splitupdate/splitupdate.go:14 names db/model/) and may write law text: " +
			"UPDATE article_version, its columns not in the same string"},
		"consttable": {"package consttable reads model output (consttable/consttable.go:15 names db/model/) and may write law text: names table amendment"},
	}
	for pkg, w := range want {
		if strings.Join(got[pkg], "\n") != strings.Join(w, "\n") {
			t.Errorf("%s:\n got  %q\n want %q", pkg, got[pkg], w)
		}
	}
	for pkg, g := range got {
		if want[pkg] == nil {
			t.Errorf("flagged %s, which writes no law text: %q", pkg, g)
		}
	}
}

// Declared a decision package, the decisions fixture may be imported by a
// writer, and its exported functions that return free text are flagged:
// a plain string, and a struct of its own with a string field. A named
// string (State) is a decision's value.
func TestScanWritersDecisionPackage(t *testing.T) {
	old := provenance.DecisionPackages
	provenance.DecisionPackages = append(old[:len(old):len(old)], fixtures+"decisions")
	defer func() { provenance.DecisionPackages = old }()
	got := map[string][]string{}
	for _, f := range provenance.ScanWriters("testdata/writers") {
		pkg, _, _ := strings.Cut(f.Path, "/")
		got[pkg] = append(got[pkg], f.String())
	}
	want := []string{
		"decisions/decisions.go:29: decision package decisions: exported Full returns free text (Answer: string)",
		"decisions/decisions.go:26: decision package decisions: exported Wording returns free text (string)",
	}
	if strings.Join(got["decisions"], "\n") != strings.Join(want, "\n") {
		t.Errorf("decisions:\n got  %q\n want %q", got["decisions"], want)
	}
	if v := got["viadecision"]; len(v) != 1 || !strings.Contains(v[0], "outside the build's writers") {
		t.Errorf("viadecision: %q, want only the write outside the writers", v)
	}
}

// The build reads model output only through internal/modelout, a decision
// package: what it hands the writers back is decisions, never text.
func TestModeloutIsTheBuildsDoor(t *testing.T) {
	if !slices.Contains(provenance.DecisionPackages, "github.com/taktekhq/lebanese-legislation/internal/modelout") {
		t.Fatal("internal/modelout is not a decision package")
	}
	for _, f := range provenance.ScanWriters("../modelout") {
		t.Error(f)
	}
}

// The reviewer's probe: a file added to internal/modelout that writes law
// text through the package's own copyRows, or by an UPDATE whose column is
// joined on, is flagged, though modelout is a decision package.
func TestModeloutProbeFlagged(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "modelout")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/taktekhq/lebanese-legislation\n"), 0o644)
	files, _ := filepath.Glob("../modelout/*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, _ := os.ReadFile(f)
		os.WriteFile(filepath.Join(dir, filepath.Base(f)), raw, 0o644)
	}
	if fs := provenance.ScanWriters(root); len(fs) != 0 {
		t.Fatalf("modelout as it stands: %v", fs)
	}
	for _, probe := range []struct{ code, want string }{
		{`copyRows(ctx, conn, "legislation_article", []string{"law_id", "number", "text_ar"}, nil)`, "legislation_article passed to copyRows"},
		{`q := "UPDATE legislation_article SET "; conn.Exec(ctx, q+"text_ar = $1", "")`, "UPDATE legislation_article, its columns not in the same string"},
	} {
		os.WriteFile(filepath.Join(dir, "zz_probe.go"), []byte("package modelout\n\nimport (\n\t\"context\"\n\n\t\"github.com/jackc/pgx/v5\"\n)\n\n"+
			"func probe(ctx context.Context, conn *pgx.Conn) {\n\t"+probe.code+"\n}\n"), 0o644)
		fs := provenance.ScanWriters(root)
		if len(fs) != 1 || fs[0].Path != "internal/modelout/zz_probe.go" || !strings.HasSuffix(fs[0].Reason, "may write law text: "+probe.want) {
			t.Errorf("probe %s: %v", probe.code, fs)
		}
	}
}

// A writer file that reads model output is flagged even though it may write,
// as is its package.
func TestScanWritersWriterReadingModel(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "build")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "amend.go"), []byte(`package build

import "github.com/taktekhq/lebanese-legislation/internal/judge"

var _ = judge.Key

func (b *builder) save(rows [][]any) error {
	_, err := b.conn.CopyFrom(b.ctx, pgx.Identifier{"article_version"}, nil, pgx.CopyFromRows(rows))
	return err
}
`), 0o644)
	os.WriteFile(filepath.Join(dir, "other.go"), []byte(`package build

func (b *builder) fix() { b.conn.Exec(b.ctx, "UPDATE legislation_article SET text_ar = $1 WHERE law_id = $2", "", 1) }
`), 0o644)
	fs := provenance.ScanWriters(root)
	if len(fs) != 2 || fs[0].Path != "internal/build/amend.go" || !strings.Contains(fs[0].Reason, "imports github.com/taktekhq/lebanese-legislation/internal/judge") ||
		fs[1].Path != "internal/build/other.go" || !strings.Contains(fs[1].Reason, "legislation_article.text_ar outside the build's writers") {
		t.Errorf("findings: %v", fs)
	}
}

// Export, the Parquet export's column query and the snapshot's schema list no
// provenance column; legislation_scan's own source_id stays public.
func TestNoProvenanceColumnPublished(t *testing.T) {
	got := export.Public("legislation_article", []string{"law_id", "text_ar", "source_kind", "source_id"})
	if strings.Join(got, ",") != "law_id,text_ar" {
		t.Errorf("export keeps %v", got)
	}
	if got := export.Public("legislation_scan", []string{"law_id", "source", "source_id"}); len(got) != 3 {
		t.Errorf("export drops legislation_scan's source_id: %v", got)
	}
	d1 := snapshot.Schema()
	if strings.Contains(d1, "source_kind") || strings.Contains(d1, "source_id") {
		t.Error("the D1 snapshot schema has a provenance column")
	}
	hf, err := os.ReadFile("../../.github/scripts/data-parquet.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hf), "AND NOT (table_name IN ('legislation_article', 'article_version') AND column_name IN ('source_kind', 'source_id'))") {
		t.Error("data-parquet.sh's column query does not leave out the provenance columns")
	}
}
