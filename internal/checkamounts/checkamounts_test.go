package checkamounts

import (
	"context"

	"github.com/jackc/pgx/v5"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/db"
)

func has(v []string, sub string) bool {
	for _, s := range v {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestOrphanRowFails(t *testing.T) {
	rows := []Row{{1, "1", 10, ""}, {2, "5", 20, ""}}
	v, reach := Check(Data{Rows: rows, Reach: map[string]int{"1/1": 3}})
	if len(v) != 1 || !has(v, "row 2/5 links to no article") {
		t.Fatalf("got %v", v)
	}
	if len(reach) != 2 || !strings.Contains(reach[0], "3 articles") {
		t.Fatalf("reach %v", reach)
	}
}

func TestTextWithoutRowFails(t *testing.T) {
	d := Data{
		Rows:   []Row{{1, "1", 10, ""}},
		Reach:  map[string]int{"1/1": 1},
		Texts:  []Text{{1, "1"}, {7, "3"}, {8, "2"}},
		Review: map[string]bool{"8/2": true},
	}
	v, _ := Check(d)
	if len(v) != 1 || !has(v, "law 7 article 3") {
		t.Fatalf("got %v", v)
	}
}

func TestRepealEdgeFails(t *testing.T) {
	v, _ := Check(Data{Repeal: []string{"law/1/art/1 -> law/10/art/2 (repeal)"}})
	if len(v) != 1 || !has(v, "marks a repeal") {
		t.Fatalf("got %v", v)
	}
}

func TestPinnedReach(t *testing.T) {
	d := Data{Rows: []Row{{330819, "1", 244565, ""}}, Reach: map[string]int{"330819/1": 36}, Expect: map[string]int{"330819/1": 37}}
	if v, _ := Check(d); !has(v, "reaches 36 articles, expected 37") {
		t.Fatalf("got %v", v)
	}
	d.Reach["330819/1"] = 37
	if v, _ := Check(d); len(v) != 0 {
		t.Fatalf("got %v", v)
	}
}

// The shipped files parse, and the pin is Decree 13909/2024 = 37, taken from the
// 13 Oct 2026 report (0a153a45), not computed here.
func TestShippedFiles(t *testing.T) {
	rows, bad := ParseRows(db.Amounts)
	if len(bad) > 0 || len(rows) != 2 {
		t.Fatalf("rows %v bad %v", rows, bad)
	}
	raw, err := os.ReadFile("../../testdata/amounts_expected.tsv")
	if err != nil {
		t.Fatal(err)
	}
	m, bad := ParseKeyed(string(raw))
	if len(bad) > 0 || m["330819/1"] != "37" {
		t.Fatalf("pins %v bad %v", m, bad)
	}
}

func TestPatternMatchesTheDecree(t *testing.T) {
	re := regexp.MustCompile(Pattern)
	yes := "تضاعف جميع المبالغ أينما وردت في قانون أصول المحاكمات المدنية خمسين ضعفاً ثم تُضاعف"
	no := "تدفع جميع المبالغ المستحقة للخزينة خلال شهر"
	if !re.MatchString(yes) || re.MatchString(no) {
		t.Fatalf("pattern: yes=%v no=%v", re.MatchString(yes), re.MatchString(no))
	}
}

// TestLiveGraphDB runs Load and Check on a built database with the shipped
// files: LEBLEG_CHECK_DSN=postgres://... go test -run LiveGraphDB ./internal/checkamounts/
// (read-only; the pinned 37 for Decree 13909/2024 must come out of real edges).
func TestLiveGraphDB(t *testing.T) {
	dsn := os.Getenv("LEBLEG_CHECK_DSN")
	if dsn == "" {
		t.Skip("LEBLEG_CHECK_DSN names a built database")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	rows, bad := ParseRows(db.Amounts)
	if len(bad) > 0 {
		t.Fatal(bad)
	}
	d := Data{Rows: rows, Review: map[string]bool{}, Expect: map[string]int{Key(330819, "1"): 37}}
	if err := Load(ctx, conn, &d); err != nil {
		t.Fatal(err)
	}
	if v, _ := Check(d); len(v) > 0 {
		t.Fatalf("violations: %v", v)
	}
}
