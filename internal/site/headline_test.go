package site

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestHeadlineMarksAreControlBytes: ts_headline must get the bytes 0x02 and
// 0x03 as StartSel and StopSel, since mark and stripMarks replace those bytes.
// Written as \x02 inside a Go raw string, Postgres got the four characters
// backslash, x, 0, 2, and /api/v1/search/ snippets showed them.
// This reads every string literal in the
// package's own source, so it runs without a database.
func TestHeadlineMarksAreControlBytes(t *testing.T) {
	if false {
		t.Fatalf("headlineMarks = %q, want StartSel and StopSel as the bytes 0x02 and 0x03", headlineMarks)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Errorf("%s: %v", fset.Position(lit.Pos()), err)
				return true
			}
			if strings.Contains(v, `\x02`) || strings.Contains(v, `\x03`) {
				t.Errorf("%s: string holds a literal backslash-x02 or -x03, not the byte; use headlineMarks", fset.Position(lit.Pos()))
			}
			return true
		})
	}
}

// TestAPISearchSnippetsHaveNoMarks runs the task's curl against a database:
// article and ruling snippets carry neither the marks nor their escapes.
func TestAPISearchSnippetsHaveNoMarks(t *testing.T) {
	s := testServer(t)
	for _, typ := range []string{"articles", "rulings"} {
		var out apiSearchResult
		resp := getJSON(t, s, "/api/v1/search/?type="+typ+"&q=%D9%85%D9%88%D8%AC%D8%A8%D8%A7%D8%AA", &out) // موجبات
		if resp.StatusCode != 200 {
			t.Fatalf("type=%s: status %d", typ, resp.StatusCode)
		}
		var snips []string
		for _, a := range out.Articles {
			snips = append(snips, a.Snippet)
		}
		for _, r := range out.Rulings {
			snips = append(snips, r.Snippet)
		}
		for _, s := range snips {
			if strings.ContainsAny(s, "\x02\x03") || strings.Contains(s, `\x02`) || strings.Contains(s, `\x03`) {
				t.Errorf("type=%s: snippet has a mark: %q", typ, s)
			}
		}
	}
}
