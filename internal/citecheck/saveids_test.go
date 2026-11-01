package citecheck

import (
	"path/filepath"
	"testing"
)

// The nightly passes -prev scraper/data/state/law-ids.prev; on 29 Oct the
// state directory did not exist and the check exited 2.
func TestSaveIDsCreatesDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "law-ids.prev")
	if err := SaveIDs(path, []int{7, 42}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadIDs(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[7] || !got[42] {
		t.Fatalf("ReadIDs = %v, want {7, 42}", got)
	}
}
