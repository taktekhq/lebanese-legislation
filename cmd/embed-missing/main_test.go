package main

import (
	"strings"
	"testing"
)

func TestReadMissing(t *testing.T) {
	rows, err := readMissing(strings.NewReader("id,hash\na,h1\nb,h2\n"), 2)
	if err != nil || len(rows) != 2 || rows[1] != (missingChunk{ID: "b", Hash: "h2"}) {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	for _, input := range []string{"hash,id\nh,a\n", "id,hash\na,\n", "id,hash\na,h\na,h\n", "id,hash\na\n"} {
		if _, err := readMissing(strings.NewReader(input), 10); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	rows, err = readMissing(strings.NewReader("id,hash\n"), 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty rows=%v err=%v", rows, err)
	}
}

func TestReadMissingRefusesCap(t *testing.T) {
	for _, cap := range []int{0, 1, -1} {
		if _, err := readMissing(strings.NewReader("id,hash\na,h1\nb,h2\n"), cap); err == nil {
			t.Fatalf("accepted cap %d", cap)
		}
	}
}
