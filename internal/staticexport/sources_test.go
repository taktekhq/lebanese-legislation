package staticexport

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The planner's source list comes from go list, so a package the site imports
// (internal/text, internal/page...) renders the whole site when it changes,
// and a test or a note beside the code does not.
func TestRenderSources(t *testing.T) {
	s, err := renderSources(context.Background(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"cmd/static-export/main.go",
		"cmd/build-db/main.go",
		"internal/site/site.go",
		"internal/site/templates/layout.html",
		"internal/site/i18n/en.json",
		"internal/staticexport/changes.go",
	} {
		if !s.files[p] {
			t.Errorf("%s is not a source", p)
		}
	}
	for _, pkg := range []string{"internal/text/", "internal/page/", "internal/site/topics/"} {
		found := false
		for f := range s.files {
			if strings.HasPrefix(f, pkg) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no source under %s", pkg)
		}
	}
	for f := range s.files {
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, ".md") {
			t.Errorf("%s is listed as a source", f)
		}
	}
	for _, c := range []struct {
		path    string
		deleted bool
		want    bool
	}{
		{"internal/site/handlers.go", false, true},
		{"internal/site/handlers_test.go", false, false},
		{"internal/site/README.md", false, false},
		{"internal/site/gone.go", true, true},
		{"internal/site/topics/gone.json", true, true},
		{"internal/site/gone_test.go", true, false},
		{"internal/site/NOTES.md", true, false},
		{"workers/static-site/src/index.js", false, false},
		{"workers/static-site/src/gone.js", true, false},
	} {
		if got := s.changes(c.path, c.deleted); got != c.want {
			t.Errorf("changes(%s, deleted=%v) = %v, want %v", c.path, c.deleted, got, c.want)
		}
	}
}
