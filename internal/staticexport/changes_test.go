package staticexport

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A nightly fixture: one edited law, one deleted decree, an article rename,
// and links through a ruling, a Gazette issue, a code and a related law.
func TestChangedRowsToPaths(t *testing.T) {
	d := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = d
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
	}
	write := func(table, cols, body string) {
		t.Helper()
		p := filepath.Join(d, "data/tables", table)
		os.MkdirAll(p, 0755)
		os.WriteFile(filepath.Join(p, "COLUMNS"), []byte(cols), 0644)
		os.WriteFile(filepath.Join(p, "000.tsv"), []byte(body), 0644)
	}
	git("init", "-q")
	git("config", "user.email", "fixture@example.org")
	git("config", "user.name", "Fixture")
	write("legislation", "id,kind,issued_on,title", "1\tقانون\t2025-01-01\tOld\n2\tمرسوم\t2024-01-01\tDeleted\n3\tقانون\t2025-02-01\tRelated\n4\tقرار\t1999-01-01\tUnrelated\n")
	write("legislation_article", "law_id,position,number", "1\t1\told number\n2\t1\t1\n3\t1\t1\n4\t1\t1\n")
	write("ruling", "id,year", "10\t2025\n")
	write("graph_edge", "from_type,from_id,to_law_id", "ruling\t10\t1\nlegislation\t3\t1\n")
	write("gazette_issue", "id,published_on", "20\t2025-03-01\n")
	write("gazette_entry", "issue_id,law_id", "20\t1\n")
	write("code", "slug,law_id", "fixture-code\t1\n")
	git("add", ".")
	git("commit", "-qm", "before")
	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = d
	b, _ := c.Output()
	before := strings.TrimSpace(string(b))
	write("legislation", "id,kind,issued_on,title", "1\tقانون\t2025-01-01\tNew\n3\tقانون\t2025-02-01\tRelated\n4\tقرار\t1999-01-01\tUnrelated\n")
	write("legislation_article", "law_id,position,number", "1\t1\tnew number\n3\t1\t1\n4\t1\t1\n")
	git("add", ".")
	git("commit", "-qm", "nightly")
	p, err := PlanChanges(context.Background(), d, before, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Full {
		t.Fatal("unexpected full render")
	}
	for _, s := range []string{"/law/1/", "/law/1/article/old-number/", "/law/1/article/new-number/", "/decree/2/", "/decree/2/article/1/", "/law/3/", "/ruling/10/", "/gazette/20/", "/laws/year/2024/", "/laws/year/2025/law/", "/codes/fixture-code/", "/changes/", "/sitemap.xml"} {
		if !slices.Contains(p.Paths, s) || !slices.Contains(p.Paths, "/en"+s) && s != "/sitemap.xml" {
			t.Errorf("missing %s (or English twin)", s)
		}
	}
	if slices.Contains(p.Paths, "/text/4/") {
		t.Error("rendered unrelated text")
	}
	for _, path := range p.Paths { // lists store their first page only (internal/site, keyset.go)
		if strings.Contains(path, "/page/") && !strings.Contains(path, "/codes/") {
			t.Errorf("planned a numbered list page %s", path)
		}
	}
	if len(p.Paths) != 90 {
		t.Fatalf("nightly fixture: got %d paths, want 90", len(p.Paths))
	}
	t.Logf("nightly fixture: %d paths", len(p.Paths))
	none, err := PlanChanges(context.Background(), d, "HEAD", false)
	if err != nil || none.Full || len(none.Paths) != 0 {
		t.Fatalf("no-op: %+v %v", none, err)
	}
	for _, base := range []string{"", "missing-commit"} {
		p, e := PlanChanges(context.Background(), d, base, false)
		if e != nil || !p.Full {
			t.Fatalf("missing baseline: %+v %v", p, e)
		}
	}
	os.MkdirAll(filepath.Join(d, "internal/site"), 0755)
	os.WriteFile(filepath.Join(d, "internal/site/render.go"), []byte("package site"), 0644)
	git("add", ".")
	git("commit", "-qm", "renderer")
	p, err = PlanChanges(context.Background(), d, before, false)
	if err != nil || !p.Full {
		t.Fatalf("renderer must force full: %+v %v", p, err)
	}
}

func TestEmptyPathsAndShardDeletion(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone/" {
			http.NotFound(w, r)
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
	})
	prev := map[string]Entry{"/gone/": {Path: "/gone/", Status: 200, File: "gone/index.html"}, "/keep/": {Path: "/keep/", Status: 200, File: "keep/index.html"}}
	for _, paths := range [][]string{{}, {"/gone/"}} {
		out := t.TempDir()
		st, e := Run(context.Background(), h, Options{Out: out, Paths: paths, PrevEntries: prev, Shard: Owner("/gone/", 2), Shards: 2})
		if e != nil {
			t.Fatal(e)
		}
		if len(paths) > 0 && !slices.Contains(st.RemovedKeys, "gone/index.html") {
			t.Fatal("shard did not delete removed page")
		}
		m, e := ReadManifest(filepath.Join(out, ".export/manifest.jsonl"))
		if e != nil {
			t.Fatal(e)
		}
		if Owner("/keep/", 2) == Owner("/gone/", 2) && m["/keep/"].Status != 200 {
			t.Fatal("forgot untouched page")
		}
	}
}

func TestRowOwners(t *testing.T) {
	for _, tc := range []struct {
		table string
		row   changeRow
		want  []string
	}{
		{"legislation", changeRow{"id": "1"}, []string{"law/1"}},
		{"legislation_article", changeRow{"law_id": "1"}, []string{"law/1"}},
		{"article_version", changeRow{"law_id": "1"}, []string{"law/1"}},
		{"amendment", changeRow{"by_law_id": "2", "law_id": "1"}, []string{"law/1", "law/2"}},
		{"graph_edge", changeRow{"from_type": "ruling", "from_id": "3", "to_law_id": "1"}, []string{"ruling/3", "law/1"}},
		{"reference", changeRow{"from_type": "legislation", "from_id": "2", "law_id": "1"}, []string{"law/2", "law/1"}},
		{"gazette_entry", changeRow{"issue_id": "4", "law_id": "1"}, []string{"gazette/4", "law/1"}},
		{"ruling_page", changeRow{"ruling_id": "3"}, []string{"ruling/3"}},
		{"noindex_page", changeRow{"kind": "english", "id": "1"}, []string{"law/1"}},
		{"page_state", changeRow{"kind": "law", "id": "1"}, []string{"law/1"}},
		{"source_page", changeRow{"doc_type": "law", "centre_id": "1"}, []string{"law/1"}},
		{"agreement", changeRow{"id": "5", "ratifying_law_id": "1"}, []string{"agreement/5", "law/1"}},
		{"legislation_alias", changeRow{"id": "6", "law_id": "1"}, []string{"law/6", "law/1"}},
	} {
		t.Run(tc.table, func(t *testing.T) {
			if got := rowEntities(tc.table, tc.row); !slices.Equal(got, tc.want) {
				t.Fatalf("%v, want %v", got, tc.want)
			}
		})
	}
}

func TestPartsAndObsoletePagination(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/law/1/" {
			t.Errorf("unexpected discovery request %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<a href="/law/1/part/2/">new part</a><a href="/law/99/">unrelated</a>`))
	})
	prev := map[string]Entry{"/law/1/part/3/": {}, "/laws/year/2025/page/2/": {}, "/law/1/article/100/": {}}
	ps, e := ExpandChangedPaths(h, []string{"/law/1/", "/laws/year/2025/"}, prev)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"/law/1/part/2/", "/law/1/part/3/", "/laws/year/2025/page/2/"} {
		if !slices.Contains(ps, p) {
			t.Errorf("missing %s", p)
		}
	}
	for _, p := range []string{"/law/99/", "/law/1/article/100/"} {
		if slices.Contains(ps, p) {
			t.Errorf("crawled unselected %s", p)
		}
	}
	empty, e := ExpandChangedPaths(h, []string{}, prev)
	if e != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty selection: %v %v", empty, e)
	}
}

func TestFullShardDeletesUnvisitedOwnedPages(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<sitemapindex/>`))
	})
	out := t.TempDir()
	prev := map[string]Entry{"/gone/": {Path: "/gone/", Status: 200, File: "gone/index.html"}, "/foreign/": {Path: "/foreign/", Status: 200, File: "foreign/index.html"}}
	shard := Owner("/gone/", 24)
	st, e := Run(context.Background(), h, Options{Out: out, PrevEntries: prev, Shard: shard, Shards: 24})
	if e != nil {
		t.Fatal(e)
	}
	if !slices.Contains(st.RemovedKeys, "gone/index.html") {
		t.Fatal("full shard kept unvisited page")
	}
	if Owner("/foreign/", 24) != shard && slices.Contains(st.RemovedKeys, "foreign/index.html") {
		t.Fatal("deleted a foreign key")
	}
}

func TestCopyTextMetadata(t *testing.T) {
	r, e := copyRow([]string{"law_id", "number", "issued_on"}, "1\tالفصل\\t1\\n\\\\\t\\N")
	if e != nil || r["number"] != "الفصل\t1\n\\" || r["issued_on"] != "" {
		t.Fatalf("%v %v", r, e)
	}
	if _, e = copyRow([]string{"id", "kind"}, "1"); e == nil {
		t.Fatal("accepted malformed COPY row")
	}
}
