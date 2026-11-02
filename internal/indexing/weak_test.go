package indexing

import (
	"path/filepath"
	"testing"
)

func TestParsePopular(t *testing.T) {
	ids, err := ParsePopular("# header\n\n12\n 7 \n12\n", "x")
	if err != nil || len(ids) != 2 || ids[0] != 12 || ids[1] != 7 {
		t.Fatalf("got %v, %v", ids, err)
	}
	for _, bad := range []string{"# only a comment\n", "12\nlaw/7\n", "-3\n"} {
		if _, err := ParsePopular(bad, "x"); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// The list in the repository parses and is not cut short.
func TestPopularTextsFile(t *testing.T) {
	ids, err := ReadPopular(filepath.Join("..", "..", "data", "popular_texts.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) < 100 {
		t.Errorf("%d popular texts: the list looks truncated", len(ids))
	}
}
