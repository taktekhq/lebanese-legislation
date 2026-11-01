package checkdates

import (
	"strings"
	"testing"
	"time"

	"github.com/taktekhq/lebanese-legislation/db"
)

func d(s string) *time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &t
}

var today = *d("2026-10-25")

func has(v []string, sub string) bool {
	for _, s := range v {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestCleanFixturePasses(t *testing.T) {
	texts := []Text{{1, d("1950-01-01"), d("1950-01-05")}, {2, nil, d("1921-01-01")}}
	tsv := []Row{{2, "gazette_date", "", "placeholder", "year only"}}
	if v := Check(texts, tsv, tsv, today); len(v) != 0 {
		t.Fatalf("clean fixture failed: %v", v)
	}
}

func TestSignedAfterGazette(t *testing.T) {
	v := Check([]Text{{7, d("1960-03-10"), d("1960-03-01")}}, nil, nil, today)
	if len(v) != 1 || !has(v, "7 signed") {
		t.Fatalf("got %v", v)
	}
}

func TestIssueEarlierIsKept(t *testing.T) {
	r := []Row{{7, "issued_on", "1960-03-10", "issue-earlier", "confirmed"}}
	if v := Check([]Text{{7, d("1960-03-10"), d("1960-03-01")}}, r, r, today); len(v) != 0 {
		t.Fatalf("got %v", v)
	}
}

func TestFuture(t *testing.T) {
	v := Check([]Text{{8, d("2026-12-31"), nil}, {9, nil, d("2026-10-26")}}, nil, nil, today)
	if !has(v, "8 issued_on") || !has(v, "9 gazette_date") {
		t.Fatalf("got %v", v)
	}
	if v := Check([]Text{{10, d("2026-10-25"), nil}}, nil, nil, today); len(v) != 0 {
		t.Fatalf("today must pass: %v", v)
	}
}

func TestRowWithoutReasonOrMethod(t *testing.T) {
	rows := []Row{{3, "issued_on", "", "signature", ""}, {4, "issued_on", "", "", "x"}, {5, "issued_on", "", "guess", "x"}}
	v := Check(nil, rows, rows, today)
	if !has(v, "3 issued_on: correction has no reason") || !has(v, "4 issued_on: correction has no method") || !has(v, "5 issued_on: method") {
		t.Fatalf("got %v", v)
	}
}

func TestTSVAndTableMustMatch(t *testing.T) {
	a := Row{1, "issued_on", "1950-01-01", "signature", "x"}
	b := Row{2, "issued_on", "1950-01-01", "signature", "x"}
	g := Row{3, "issued_on", "", "guard", "x"}
	v := Check(nil, []Row{a}, []Row{b, g}, today)
	if len(v) != 2 || !has(v, "1 issued_on: in date_corrections.tsv, not in date_correction") || !has(v, "2 issued_on: in date_correction, not in") {
		t.Fatalf("got %v", v)
	}
}

func TestUncertain(t *testing.T) {
	u := Uncertain([]Row{{5, "issued_on", "", "guard", "x"}, {2, "gazette_date", "", "placeholder", "x"}, {9, "issued_on", "1950-01-01", "signature", "x"}})
	if len(u) != 2 || u[0] != 2 || u[1] != 5 {
		t.Fatalf("got %v", u)
	}
}

// The shipped file: its expected counts come from counting the file itself
// with awk on 25 Oct 2026 (497 rows; 271 with the corrected date empty).
func TestShippedTSV(t *testing.T) {
	rows, bad := ParseTSV(db.DateCorrections)
	if len(bad) != 0 {
		t.Fatal(bad)
	}
	if len(rows) < 497 {
		t.Fatalf("rows %d, want at least 497", len(rows))
	}
	if v := Check(nil, rows, rows, today); len(v) != 0 {
		t.Fatalf("shipped TSV breaks the rules: %v", v)
	}
	if n := len(Uncertain(rows)); n < 271 {
		t.Fatalf("uncertain %d, want at least 271", n)
	}
}
