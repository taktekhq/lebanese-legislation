package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/checkversions"
)

func TestDefaultDatabaseDSN(t *testing.T) {
	t.Setenv("LEBLEG_DSN", "")
	if got := defaultDatabaseDSN(); got != "host=/tmp dbname=lebleg" {
		t.Fatalf("defaultDatabaseDSN() = %q, want local database", got)
	}
	t.Setenv("LEBLEG_DSN", "host=db dbname=copy")
	if got := defaultDatabaseDSN(); got != "host=db dbname=copy" {
		t.Fatalf("defaultDatabaseDSN() = %q, want environment DSN", got)
	}
}

// A missing baseline is reported as missing in both modes, so the check-only
// run of the nightly report still checks the articles (27 Oct 2026: it exited
// 2 on the missing file and hid 609 violations); only the live run writes it.
func TestReadPendingBaselineMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "versions-pending.txt")
	for _, checkOnly := range []bool{false, true} {
		prev, missing, err := readPendingBaseline(path, checkOnly)
		if err != nil || !missing || prev != 0 {
			t.Fatalf("readPendingBaseline(checkOnly=%t) = %d, %t, %v; want 0, true, nil", checkOnly, prev, missing, err)
		}
	}
	if err := keepPendingBaseline(path, 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("check-only wrote the baseline: %v", err)
	}
}

func TestReadPendingBaselineUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions-pending.txt")
	if err := os.WriteFile(path, []byte("many\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPendingBaseline(path, true); err == nil {
		t.Fatal("check-only accepted an unreadable pending baseline")
	}
}

func TestReadPendingBaselineValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions-pending.txt")
	if err := os.WriteFile(path, []byte("165\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev, missing, err := readPendingBaseline(path, false)
	if err != nil || missing || prev != 165 {
		t.Fatalf("readPendingBaseline() = %d, %t, %v; want 165, false, nil", prev, missing, err)
	}
}

func TestKeepPendingBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions-pending.txt")
	if err := os.WriteFile(path, []byte("100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := keepPendingBaseline(path, 102, true); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "100\n" {
		t.Fatalf("check-only changed pending baseline to %q", got)
	}
	if err := keepPendingBaseline(path, 102, false); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "102\n" {
		t.Fatalf("normal check left pending baseline %q", got)
	}
}

func TestKeepPendingBaselineCreatesDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipeline", "versions-pending.txt")
	if err := keepPendingBaseline(path, 17, false); err != nil {
		t.Fatalf("keepPendingBaseline() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "17\n" {
		t.Fatalf("baseline = %q, want 17", got)
	}
}

func TestKeepPendingBaselinePropagatesWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file", "versions-pending.txt")
	if err := os.WriteFile(filepath.Dir(path), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := keepPendingBaseline(path, 1, false); err == nil {
		t.Fatal("expected write failure")
	}
}

func TestKeepPendingBaselineCheckOnlyDoesNotNeedWritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "versions-pending.txt")
	if err := keepPendingBaseline(path, 1, true); err != nil {
		t.Fatalf("check-only should not write baseline: %v", err)
	}
}

// This test's comparison values come from the sourced fixture on disk; see
// testdata/checkversions/art73_law321_2023.txt and its Gazette citation.
func TestSourcedArticle73FixtureHasExpectedPhrases(t *testing.T) {
	f, err := os.Open("../../testdata/checkversions/art73_law321_2023.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	phrases, err := checkversions.ReadFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(phrases) != 8 {
		t.Fatalf("fixture phrases = %d, want Gazette-sourced eight phrases", len(phrases))
	}
	article := checkversions.Article{LawID: checkversions.Art73Law, Number: checkversions.Art73Number,
		InView: true, State: "in_force", Text: "الادلاء بالدفوع الشكلية ولـمرة واحدة، قبل استجواب الـمدعى عليه الدفع بقوة القضية الـمحكوم بها " +
			"الدفع ببطلان إجراء أو أكثر من إجراءات التحقيق ولكل منهما مهلة أسبوعين من تاريخ تبليغه لتقديم ملاحظاته على الدفوع الـمثارة " +
			"ودون أن يحق له ضم الدفع الى الاساس بنصف الحد الأدنى الرسمي للأجور على الأقل وبعشرة أضعافه على الأكثر " +
			"تسري أحكام هذه الـمادة أمام جميع الـمراجع القضائية الجزائية"}
	result := checkversions.Check([]checkversions.Article{article}, phrases)
	if len(result.Violations) != 0 {
		t.Fatalf("sourced Article 73 wording did not pass: %v", result.Violations)
	}
}

func TestSourcedFixtureArticle73OldTextFails(t *testing.T) {
	f, err := os.Open("../../testdata/checkversions/art73_law321_2023.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	phrases, err := checkversions.ReadFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	article := checkversions.Article{LawID: checkversions.Art73Law, Number: checkversions.Art73Number,
		InView: true, State: "in_force", Text: "للمدعى عليه أن يدلي بالدفوع الشكلية في أي وقت قبل الحكم"}
	result := checkversions.Check([]checkversions.Article{article}, phrases)
	if len(result.Violations) != len(phrases) {
		t.Fatalf("old Article 73 text produced %d violations, want %d: %v", len(result.Violations), len(phrases), result.Violations)
	}
}
