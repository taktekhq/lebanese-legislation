package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/staticexport"
)

// The Worker joins pages stored in parts back into their frame. This
// exports the pages that need no database twice, whole and in parts, and
// asks the Worker's own code (node --test) for every page of the first over
// a bucket made of the second: the bytes must be the same.
func TestEdgeLayoutThroughWorker(t *testing.T) {
	node := workerNode(t)
	srv, err := newSite(nil, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	paths := []string{"/blog/", "/en/blog/", "/ai/", "/en/ai/", "/contribute/", "/en/contribute/", "/run-it-yourself/", "/en/run-it-yourself/", "/robots.txt"}
	ctx := context.Background()
	whole := filepath.Join(dir, "whole")
	if _, err := staticexport.Run(ctx, srv, staticexport.Options{Out: whole, Paths: paths}); err != nil {
		t.Fatal(err)
	}
	parts := filepath.Join(dir, "parts")
	layout, err := publishLayout(ctx, srv, nil, parts, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	st, err := staticexport.Run(ctx, srv, staticexport.Options{Out: parts, Paths: paths, Layout: layout})
	if err != nil {
		t.Fatal(err)
	}
	if st.Parts != 8 {
		t.Fatalf("%d pages in parts, want 8", st.Parts)
	}
	cmd := exec.Command(node, "--test", "test/edgelayout.test.mjs")
	cmd.Dir = filepath.Join("..", "..", "workers", "static-site")
	cmd.Env = append(os.Environ(), "EDGE_LAYOUT_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil || !regexp.MustCompile(`8 of 8 HTML pages identical`).Match(out) {
		t.Fatalf("worker test: %v\n%s", err, out)
	}
}

// workerNode is a node that can run the Worker's tests (module hooks need
// 20.6 or later): $LEBLEG_NODE, else node on the PATH. The test is skipped
// without one.
func workerNode(t *testing.T) string {
	t.Helper()
	node := os.Getenv("LEBLEG_NODE")
	if node == "" {
		var err error
		if node, err = exec.LookPath("node"); err != nil {
			t.Skip("no node to run the Worker with")
		}
	}
	v, err := exec.Command(node, "-p", "process.versions.node").Output()
	if err != nil {
		t.Skipf("%s: %v", node, err)
	}
	m := regexp.MustCompile(`^(\d+)\.(\d+)`).FindSubmatch(v)
	if m == nil {
		t.Skipf("node version %q", v)
	}
	major, _ := strconv.Atoi(string(m[1]))
	minor, _ := strconv.Atoi(string(m[2]))
	if major < 20 || major == 20 && minor < 6 {
		t.Skipf("node %s is older than 20.6; set LEBLEG_NODE", v)
	}
	return node
}
