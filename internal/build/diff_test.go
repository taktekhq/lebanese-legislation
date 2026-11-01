package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareRowsAndExpectedDiff(t *testing.T) {
	old := map[string][]byte{
		"legislation_article": []byte("1\t1\told\n2\t1\tkept\n"),
		"amendment":           []byte("10\told\n"),
		"article_version":     []byte("1\t2\t1\t0\told\n"),
	}
	newRows := map[string][]byte{
		"legislation_article": []byte("1\t1\tnew\n2\t1\tkept\n3\t1\tadded\n"),
		"amendment":           []byte("10\tnew\n"),
		"article_version":     []byte("1\t2\t1\t0\tnew\n"),
	}
	got, err := CompareRows(old, newRows)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.changed) != 3 || len(got.added) != 1 || len(got.removed) != 0 {
		t.Fatalf("diff counts: changed %d added %d removed %d", len(got.changed), len(got.added), len(got.removed))
	}
	for table, key := range map[string]string{
		"legislation_article": "1\x1f1",
		"amendment":           "10",
		"article_version":     "1\x1f2\x1f1\x1f0",
	} {
		if _, ok := got.changed[diffKey{table, key}]; !ok {
			t.Errorf("missing changed %s key %q", table, key)
		}
	}
	if _, ok := got.added[diffKey{"legislation_article", "3\x1f1"}]; !ok {
		t.Error("missing added article")
	}
	t.Run("expected row passes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "report.txt")
		want := []expectedRow{{"legislation_article", "1\x1f1", "source fixture article changed"}, {"legislation_article", "3\x1f1", "source fixture article added"}, {"amendment", "10", "source fixture amendment changed"}, {"article_version", "1,2,1,0", "source fixture version changed"}}
		if err := writeDiffReport(path, got, want); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "PASS: actual diff matches expectation") {
			t.Fatalf("report: %s", b)
		}
	})
	t.Run("missing expected row fails", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "report.txt")
		err := writeDiffReport(path, got, []expectedRow{{"legislation_article", "1\x1f1", "only one"}})
		if err == nil || !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestCompareRowsRejectsDuplicateAndMalformedData(t *testing.T) {
	for name, data := range map[string][]byte{
		"duplicate": []byte("1\t1\ta\n1\t1\tb\n"),
		"malformed": []byte("1\n"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := CompareRows(map[string][]byte{"legislation_article": data}, map[string][]byte{})
			if err == nil {
				t.Fatal("expected invalid rows to fail")
			}
		})
	}
}

func TestReadExpectedUsesSourceRows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "expected.tsv")
	if err := os.WriteFile(p, []byte("legislation_article\t1,1\tUL source test fixture: one article wording changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err := readExpected(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].key != "1,1" || rows[0].reason == "" {
		t.Fatalf("unexpected expected rows: %#v", rows)
	}
	if _, err := readExpected("none"); err != nil {
		t.Fatal(err)
	}
}
