package snapshot

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// sqliteRun runs each SQL file, in order, against the SQLite database at db
// through python3's sqlite3 (SQLite with FTS5, as D1 is; the repo has no Go
// SQLite driver). It prints the base tables left in sqlite_master, FTS5
// shadow tables aside.
const sqliteRun = `
import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
for f in sys.argv[2:]:
    c.executescript(open(f).read())
c.commit()
virt = [r[0] for r in c.execute("SELECT name FROM sqlite_master WHERE type='table' AND sql LIKE 'CREATE VIRTUAL TABLE%'")]
shadow = {v + "_" + s for v in virt for s in ("data", "idx", "content", "docsize", "config")}
for (n,) in c.execute("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name"):
    if n not in shadow:
        print(n)
`

func sqlite(t *testing.T, db string, files ...string) (string, error) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is needed to run SQLite: %v", err)
	}
	out, err := exec.Command(py, append([]string{"-I", "-c", sqliteRun, db}, files...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func writeSQL(t *testing.T, dir, name, sql string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The drop list names exactly the tables SQLite holds after loading
// schema.sql: SQLite's own sqlite_master is the expected value.
func TestDropSQLNamesEverySchemaTable(t *testing.T) {
	dir := t.TempDir()
	schema := writeSQL(t, dir, "schema.sql", Schema())
	got, err := sqlite(t, filepath.Join(dir, "d1.db"), schema)
	if err != nil {
		t.Fatalf("schema load: %v\n%s", err, got)
	}
	want := SchemaTables()
	sort.Strings(want)
	if got != strings.Join(want, "\n") {
		t.Fatalf("SchemaTables %v, SQLite holds:\n%s", want, got)
	}
	if !strings.Contains(DropSQL(), "DROP TABLE IF EXISTS ruling_page;\n") {
		t.Fatalf("drop.sql lacks ruling_page:\n%s", DropSQL())
	}
}

// A load that stopped halfway left ruling_page in
// the idle database, and the next run's schema.sql failed on "table
// ruling_page already exists". The clear step must leave room for the
// schema load, whether one table or the whole snapshot was left behind.
func TestDropSQLClearsLeftoverTables(t *testing.T) {
	dir := t.TempDir()
	schema := writeSQL(t, dir, "schema.sql", Schema())
	drop := writeSQL(t, dir, "drop.sql", DropSQL())
	rulingPage := Schema()[strings.Index(Schema(), "CREATE TABLE ruling_page ("):]
	rulingPage = rulingPage[:strings.Index(rulingPage, ");")+2]
	row := "\nINSERT INTO ruling_page (ruling_id, n, text_ar) VALUES (1, 1, 'x');\n"
	left := writeSQL(t, dir, "left.sql", rulingPage+row)
	full := writeSQL(t, dir, "full.sql", Schema()+row)

	// The list snapshot.sh kept by hand before 0120 reproduces the failure.
	var old strings.Builder
	for _, tb := range strings.Fields("legislation legislation_section legislation_article centre_error gazette_issue gazette_entry ruling graph_edge possibly_affected law_search law_search_en article_search article_search_fr ruling_search") {
		old.WriteString("DROP TABLE IF EXISTS " + tb + ";\n")
	}
	oldDrop := writeSQL(t, dir, "old-drop.sql", old.String())
	if out, err := sqlite(t, filepath.Join(dir, "old.db"), left, oldDrop, schema); err == nil || !strings.Contains(out, "table ruling_page already exists") {
		t.Fatalf("old drop list: want 'table ruling_page already exists', got err=%v\n%s", err, out)
	}

	all := SchemaTables()
	sort.Strings(all)
	for name, before := range map[string][]string{
		"ruling_page only": {left},
		"whole snapshot":   {full},
	} {
		t.Run(name, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "idle.db")
			if out, err := sqlite(t, db, before...); err != nil {
				t.Fatalf("setup: %v\n%s", err, out)
			}
			out, err := sqlite(t, db, drop, schema)
			if err != nil {
				t.Fatalf("drop then schema load: %v\n%s", err, out)
			}
			if out != strings.Join(all, "\n") {
				t.Fatalf("tables after reload:\n%s", out)
			}
		})
	}
}
