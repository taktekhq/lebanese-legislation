package staticexport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// renderInputs are the files outside Go packages that a render reads: the
// database load and its extra steps in .github/workflows/static-site.yml, and
// the workflow itself. A change to any of them renders the whole site.
var renderInputs = map[string]bool{
	"db/official.sql":                         true,
	"data/load.sh":                            true,
	"data/schema.sql":                         true,
	"data/indexes.sql":                        true,
	"data/TABLES":                             true,
	"data/popular_texts.txt":                  true,
	"data/article_calculated_current.jsonl":   true,
	"data/article_en.jsonl":                   true,
	"data/article_en.sql":                     true,
	"data/gazette_annex.jsonl":                true,
	"data/gazette_annex.sql":                  true,
	"go.mod":                                  true,
	"go.sum":                                  true,
	".github/workflows/static-site.yml":       true,
	".github/actions/render-setup/action.yml": true,
	".github/scripts/render-db-tag.sh":        true,
}

// renderCommands are the commands a render runs: the exporter, and build-db
// for the machine-English step.
var renderCommands = []string{"./cmd/static-export", "./cmd/build-db"}

// sources are the files the render commands are compiled from: the Go files
// and embedded files (templates, strings, topics, assets) of every package of
// this module they import, by repository path.
type sources struct {
	files map[string]bool
	dirs  []string // the packages' folders
	guess bool     // no go list: every file in dirs counts
}

// changes says whether a changed path is one of the sources. A deleted file
// is no longer listed, so any deleted file in a package folder counts, other
// than tests, notes and test data.
func (s sources) changes(p string, deleted bool) bool {
	if s.files[p] {
		return true
	}
	if !deleted && !s.guess || strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, ".md") || strings.Contains(p, "/testdata/") {
		return false
	}
	for _, d := range s.dirs {
		if strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// renderSources asks go list which files the render commands are built from.
// A folder without go.mod (a test fixture) cannot be asked, so every file in
// its internal/, cmd/ and db/ counts.
func renderSources(ctx context.Context, dir string) (sources, error) {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); errors.Is(err, os.ErrNotExist) {
		return sources{dirs: []string{"internal", "cmd", "db"}, guess: true}, nil
	} else if err != nil {
		return sources{}, err
	}
	root, err := filepath.Abs(dir)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return sources{}, err
	}
	c := exec.CommandContext(ctx, "go", append([]string{"list", "-deps", "-json=Dir,GoFiles,CgoFiles,EmbedFiles,Module"}, renderCommands...)...)
	c.Dir = dir
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		return sources{}, fmt.Errorf("go list %s: %w: %s", strings.Join(renderCommands, " "), err, stderr.Bytes())
	}
	s := sources{files: map[string]bool{}}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p struct {
			Dir                           string
			GoFiles, CgoFiles, EmbedFiles []string
			Module                        *struct{ Main bool }
		}
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return sources{}, fmt.Errorf("go list: %w", err)
		}
		if p.Module == nil || !p.Module.Main {
			continue // the standard library and other modules change with go.mod and go.sum
		}
		rel, err := filepath.Rel(root, p.Dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			return sources{}, fmt.Errorf("go list: package folder %s is outside %s", p.Dir, root)
		}
		rel = filepath.ToSlash(rel)
		s.dirs = append(s.dirs, rel)
		for _, list := range [][]string{p.GoFiles, p.CgoFiles, p.EmbedFiles} {
			for _, f := range list {
				s.files[path.Join(rel, f)] = true
			}
		}
	}
	if len(s.files) == 0 {
		return sources{}, errors.New("go list found no source files for the render commands")
	}
	return s, nil
}
