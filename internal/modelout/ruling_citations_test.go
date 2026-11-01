package modelout

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRulingCitationsJoinsFiles(t *testing.T) {
	dir := t.TempDir()
	long := filepath.Join(dir, "long.jsonl")
	short := filepath.Join(dir, "short.jsonl")
	os.WriteFile(long, []byte(`{"key":"ruling/1","model":"a"}`+"\n"+`{"key":"ruling/2","model":"a"}`+"\n"), 0o644)
	os.WriteFile(short, []byte(`{"key":"ruling/3","model":"b"}`+"\n"+`{"key":"ruling/2","model":"b"}`+"\n"), 0o644)
	cs, err := loadRulingCitations(long + "," + short + "," + filepath.Join(dir, "missing.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 3 {
		t.Fatalf("got %d citations, want 3", len(cs))
	}
	if cs["ruling/2"].Model != "b" {
		t.Errorf("ruling/2 from %q, want the later file", cs["ruling/2"].Model)
	}
}
