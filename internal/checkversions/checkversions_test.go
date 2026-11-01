package checkversions

import (
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) []string {
	f, err := os.Open("../../testdata/checkversions/art73_law321_2023.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	p, err := ReadFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 8 {
		t.Fatalf("fixture phrases = %d, want 8", len(p))
	}
	return p
}

// newText is built from the fixture's own phrases (quoted from the Gazette).
func newText(p []string) string { return strings.Join(p, " ") }

const oldText = "للمدعى عليه أن يدلي بالدفوع الشكلية في أي وقت قبل الحكم"

func good(p []string) []Article {
	return []Article{
		{LawID: 1, Number: "1", CentreText: "نص", InView: true, State: "in_force", Text: "نص"},
		{LawID: 1, Number: "2", CentreText: "قديم", InView: true, State: "in_force", Text: "جديد"},
		{LawID: 1, Number: "3", CentreText: "x", InView: true, State: "repealed"},
		{LawID: 1, Number: "4", CentreText: "x", InView: true, State: "in_force", Pending: true},
		{LawID: Art73Law, Number: "73", CentreText: oldText, InView: true, State: "in_force", Text: newText(p)},
	}
}

func TestCorrectFixturePasses(t *testing.T) {
	p := fixture(t)
	r := Check(good(p), p)
	if len(r.Violations) != 0 {
		t.Fatalf("violations: %v", r.Violations)
	}
	if r.Ahead != 2 || r.Pending != 1 || r.Total != 5 {
		t.Fatalf("ahead %d pending %d total %d, want 2 1 5", r.Ahead, r.Pending, r.Total)
	}
}

func TestArticleMissingFromView(t *testing.T) {
	p := fixture(t)
	a := good(p)
	a[0].InView = false
	r := Check(a, p)
	if len(r.Violations) != 1 || !strings.Contains(r.Violations[0], "no row in article_in_force") {
		t.Fatalf("violations: %v", r.Violations)
	}
}

func TestNoTextWhileInForce(t *testing.T) {
	p := fixture(t)
	a := good(p)
	a[0].Text = ""
	r := Check(a, p)
	if len(r.Violations) != 1 || !strings.Contains(r.Violations[0], "no current text") {
		t.Fatalf("violations: %v", r.Violations)
	}
}

func TestArticle73KeepsOldWording(t *testing.T) {
	p := fixture(t)
	a := good(p)
	a[4].Text = oldText
	r := Check(a, p)
	if len(r.Violations) != len(p) {
		t.Fatalf("want %d missing-phrase violations, got %v", len(p), r.Violations)
	}
}

func TestArticle73Absent(t *testing.T) {
	p := fixture(t)
	r := Check(good(p)[:4], p)
	if len(r.Violations) != 1 || !strings.Contains(r.Violations[0], "not found") {
		t.Fatalf("violations: %v", r.Violations)
	}
}

func TestTatweelIgnored(t *testing.T) {
	if norm("الـمدعى  عليه") != norm("المدعى عليه") {
		t.Fatal("tatweel and spacing should not matter")
	}
}

func TestPendingRose(t *testing.T) {
	cases := []struct {
		now, prev int
		want      bool
	}{
		{101, 100, false},
		{102, 100, true},
		{1, 0, true},
		{0, 0, false},
	}
	for _, tc := range cases {
		if got := PendingRose(tc.now, tc.prev); got != tc.want {
			t.Errorf("PendingRose(%d, %d) = %v, want %v", tc.now, tc.prev, got, tc.want)
		}
	}
}

func TestLatestVersionMissingDateFailsWhenAmendmentHasDate(t *testing.T) {
	p := fixture(t)
	a := good(p)
	a = append(a, Article{LawID: 326577, Number: "1", InView: true, State: "in_force", Text: "new",
		LatestAmendmentDate: "2023-07-13"})
	r := Check(a, p)
	if len(r.Violations) != 1 || !strings.Contains(r.Violations[0], "latest version date  is before applied amendment date") {
		t.Fatalf("violations: %v", r.Violations)
	}
}

func TestUnknownAmendmentDateIsNotCompared(t *testing.T) {
	p := fixture(t)
	a := good(p)
	a = append(a, Article{LawID: 326577, Number: "1", InView: true, State: "in_force", Text: "new",
		LatestVersionDate: "2020-01-01"})
	if r := Check(a, p); len(r.Violations) != 0 {
		t.Fatalf("violations: %v", r.Violations)
	}
}

func TestAheadCountOnlyCountsNonemptyInViewTextDifferentFromCentre(t *testing.T) {
	p := fixture(t)
	a := good(p)
	a = append(a,
		Article{LawID: 2, Number: "1", InView: false, Text: "different", CentreText: "centre"},
		Article{LawID: 2, Number: "2", InView: true, Text: "", CentreText: "centre"},
		Article{LawID: 2, Number: "3", InView: true, Text: "same", CentreText: "same"},
	)
	r := Check(a, p)
	if r.Ahead != 2 {
		t.Fatalf("ahead = %d, want 2", r.Ahead)
	}
}

func TestArticle73RequiresEveryFixturePhrase(t *testing.T) {
	p := fixture(t)
	a := good(p)
	a[4].Text = strings.Join(p[:len(p)-1], " ")
	r := Check(a, p)
	if len(r.Violations) != 1 || !strings.Contains(r.Violations[0], p[len(p)-1]) {
		t.Fatalf("violations: %v", r.Violations)
	}
}

func TestFixtureMalformedEmptyFails(t *testing.T) {
	if _, err := ReadFixture(strings.NewReader(" # source comment\n\n")); err == nil {
		t.Fatal("expected empty fixture error")
	}
}

func TestFixtureScannerErrorPropagates(t *testing.T) {
	tooLong := strings.Repeat("x", 70*1024)
	if _, err := ReadFixture(strings.NewReader(tooLong)); err == nil {
		t.Fatal("expected scanner error")
	}
}
func TestLatestVersionCannotPrecedeAppliedAmendment(t *testing.T) {
	p := fixture(t)
	a := good(p)
	// Fixture evidence: amendment 3 to law 326577 article 1 took effect 2023-07-13.
	a = append(a, Article{LawID: 326577, Number: "1", InView: true, State: "in_force", Text: "new",
		LatestVersionDate: "2023-07-12", LatestAmendmentDate: "2023-07-13"})
	r := Check(a, p)
	if len(r.Violations) != 1 || !strings.Contains(r.Violations[0], "latest version date 2023-07-12 is before applied amendment date 2023-07-13") {
		t.Fatalf("violations: %v", r.Violations)
	}
}

func TestLatestVersionOnAmendmentDatePasses(t *testing.T) {
	p := fixture(t)
	a := good(p)
	a = append(a, Article{LawID: 326577, Number: "1", InView: true, State: "in_force", Text: "new",
		LatestVersionDate: "2023-07-13", LatestAmendmentDate: "2023-07-13"})
	if r := Check(a, p); len(r.Violations) != 0 {
		t.Fatalf("violations: %v", r.Violations)
	}
}
