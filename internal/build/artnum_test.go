package build

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The graph step, run on a loaded database (LEBLEG_DSN, as `build-db -only
// graph` left it): Law 3/2025 inserts articles 12-1 to 12-10 into the Labour
// Code (190374), and every edge to them is spelled "12-N", none "12/N". And the
// build check finds no other spelling anywhere.
func TestGraphArticleNumbersInDB(t *testing.T) {
	dsn := os.Getenv("LEBLEG_DSN")
	if dsn == "" {
		t.Skip("no LEBLEG_DSN: a database built by build-db")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("no database: %v", err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM graph_edge WHERE to_law_id = 190374 AND from_key LIKE 'law/333455/art/%'`).Scan(&n); err != nil || n == 0 {
		t.Skipf("database has no edges from Law 3/2025 to the Labour Code (%v)", err)
	}
	rows, err := conn.Query(ctx, `SELECT to_article, source, to_key FROM graph_edge
		WHERE to_law_id = 190374 AND from_key LIKE 'law/333455/art/%' AND to_article ~ '^12[^0-9]'`)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]map[string]bool{} // article -> sources
	for rows.Next() {
		var art, src, key string
		if err := rows.Scan(&art, &src, &key); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(art, "/") || strings.Contains(art, ".") || strings.Contains(art, " ") && !strings.Contains(art, "مكرر") {
			t.Errorf("edge to %q", art)
		}
		if key != "law/190374/art/"+art {
			t.Errorf("edge to %q points at %q", art, key)
		}
		if seen[art] == nil {
			seen[art] = map[string]bool{}
		}
		if seen[art][src] {
			t.Errorf("article %q has two %s edges from one article", art, src)
		}
		seen[art][src] = true
	}
	rows.Close()
	for _, a := range []string{"12-1", "12-2", "12-3", "12-4", "12-5", "12-6", "12-7", "12-8", "12-9", "12-10"} {
		if !seen[a]["parser"] {
			t.Errorf("no parser edge to %s", a)
		}
	}
	c, err := checkArticleNumbers(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if c.Total() != 0 {
		t.Errorf("spellings left: %v", c.NonCanonical)
	}
}

func TestReportNotPublishableReason(t *testing.T) {
	tests := []struct {
		name   string
		report Report
		want   string
	}{
		{"no full text", Report{}, "no full texts were built"},
		{"noncanonical article number", Report{WithText: 100, CountMatch: 100, Tables: map[string]int64{"legislation": 1}, ArtCheck: ArtNumCheck{NonCanonical: map[string]int{"graph_edge.to_article": 2}, Checked: true}}, "2 article numbers are not spelled the Centre's way"},
		{"no legislation", Report{WithText: 100, CountMatch: 100, Tables: map[string]int64{}}, "no legislation rows were built"},
		{"low count agreement", Report{WithText: 100, CountMatch: 97, Tables: map[string]int64{"legislation": 1}}, "article counts agree for 97 of 100 full texts (97.00%; need at least 98%)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			tt.report.Write(&out)
			lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
			flag := -1
			for i, line := range lines {
				if line == "publishable: false" {
					flag = i
					break
				}
			}
			if flag < 0 || flag+1 >= len(lines) || lines[flag+1] != "not publishable because: "+tt.want {
				t.Fatalf("reason must immediately follow false flag; got %q", out.String())
			}
		})
	}
}

func TestReportGateArticleNumbers(t *testing.T) {
	r := &Report{WithText: 100, CountMatch: 100, Tables: map[string]int64{"legislation": 1}}
	if !r.OK() {
		t.Fatal("a clean report is publishable")
	}
	r.ArtCheck = ArtNumCheck{NonCanonical: map[string]int{"graph_edge.to_article": 1}, Checked: true}
	if r.OK() {
		t.Error("a non-canonical spelling left must stop publishing")
	}
	r.ArtCheck = ArtNumCheck{NonCanonical: map[string]int{}, NotFound: 1338, Checked: true}
	if !r.OK() {
		t.Error("edge articles the text lacks are reported only")
	}
}

// The nightly appends -only reruns to the full build's report, and the gate
// reads the last publishable: line, so a rerun must not write one.
func TestReportOnlyRerunGivesNoVerdict(t *testing.T) {
	var out strings.Builder
	(&Report{Only: "indexing"}).Write(&out)
	if strings.Contains(out.String(), "publishable:") {
		t.Fatalf("an -only rerun wrote a publishing verdict: %q", out.String())
	}
	if !strings.Contains(out.String(), "-only indexing rerun: no publishing verdict") {
		t.Fatalf("an -only rerun must say it gives no verdict: %q", out.String())
	}
}
