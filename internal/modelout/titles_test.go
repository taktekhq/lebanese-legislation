package modelout

import (
	"strconv"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/db"
)

func TestCleanTitleEn(t *testing.T) {
	for in, want := range map[string]string{
		"Amending the decision — on fees": "Amending the decision - on fees",
		"  Two   spaces\tand a tab ":      "Two spaces and a tab",
		"Court decisions":                 "Court decisions",
		"Decision on القرار":              "",
		"":                                "",
	} {
		if got := cleanTitleEn(in); got != want {
			t.Errorf("cleanTitleEn(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTitlesEnFile checks the shipped file is the shape the loader reads: a
// header, then law id, title, model; ids once each. Titles with Arabic letters
// or dashes are cleaned or dropped by the loader, not rejected here.
func TestTitlesEnFile(t *testing.T) {
	lines := strings.Split(strings.TrimRight(db.TitlesEn, "\n"), "\n")
	if lines[0] != "law_id\ttitle_en\tmodel" {
		t.Fatalf("header: %q", lines[0])
	}
	seen := map[string]bool{}
	for i, ln := range lines[1:] {
		f := strings.Split(ln, "\t")
		if len(f) != 3 {
			t.Fatalf("line %d: %d columns", i+2, len(f))
		}
		if _, err := strconv.Atoi(f[0]); err != nil || seen[f[0]] {
			t.Fatalf("line %d: bad or repeated id %q", i+2, f[0])
		}
		seen[f[0]] = true
		if f[1] == "" || f[2] == "" {
			t.Fatalf("line %d: empty field", i+2)
		}
	}
}
