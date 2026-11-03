package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestChangesCommandFirstRender(t *testing.T) {
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	if e := run(context.Background(), []string{"changes", "-out", out}, &stdout, &stderr); e != nil {
		t.Fatal(e)
	}
	mode, e := os.ReadFile(filepath.Join(out, "mode"))
	if e != nil || string(mode) != "full\n" {
		t.Fatalf("mode %q: %v", mode, e)
	}
	paths, e := readLines(filepath.Join(out, "paths.txt"))
	if e != nil || paths == nil || len(paths) != 0 {
		t.Fatalf("empty path file must be an explicit no-op: %v %v", paths, e)
	}
}
