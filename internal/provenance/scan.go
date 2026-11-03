package provenance

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Finding is a source file that may put model text into a law-text column.
type Finding struct {
	Path   string // relative to the root scanned, with forward slashes
	Line   int
	Reason string
}

func (f Finding) String() string { return fmt.Sprintf("%s:%d: %s", f.Path, f.Line, f.Reason) }

// WriterFiles are the only files that may write law text: the build's
// writers, each storing the text's source with it (internal/build/sources.go).
var WriterFiles = []string{"internal/build/load.go", "internal/build/scans.go", "internal/build/amend.go"}

// DecisionPackages read model output and may be imported by a package that
// writes law text, because what they hand back is decisions and counts,
// never a text: ScanWriters checks that no exported function or method of
// theirs returns free text (a plain string, []byte, any, or a model
// package's type). internal/modelout is the build's one door to model
// output.
var DecisionPackages = []string{"github.com/taktekhq/lebanese-legislation/internal/modelout"}

// The tables whose text columns hold law text: legislation (title,
// preamble, closing), legislation_article (text_ar, text_fr),
// article_version (text_ar) and amendment (text_ar, old_text_ar).
const textTables = `legislation|legislation_article|article_version|amendment`

var (
	sqlInsert = regexp.MustCompile(`(?is)\binsert\s+into\s+(?:public\.)?"?(` + textTables + `)\b`)
	sqlCopy   = regexp.MustCompile(`(?is)\bcopy\s+(?:public\.)?"?(` + textTables + `)\b[^;]*?\bfrom\b`)
	sqlUpdate = regexp.MustCompile(`(?is)\bupdate\s+(?:public\.)?"?(` + textTables + `)\b[^;]*?\bset\b[^;]*?\b(text_ar|text_fr|old_text_ar|title|preamble|closing)\b`)
	// An UPDATE of a text table whose columns are not in the same string
	// ("UPDATE legislation_article SET " + col).
	sqlUpdateAny = regexp.MustCompile(`(?is)\bupdate\s+(?:public\.)?"?(` + textTables + `)\b`)
	textTable    = regexp.MustCompile(`^(?:` + textTables + `)$`)
	// Model output: the model's files (db/model/), the machine readings of
	// scanned judgments (*.read.json, ruling-ocr), OCR output folders.
	modelPath = regexp.MustCompile(`db/model/|\bmodel/(?:judgements|citations|ruling_citations)|\.read\.json\b|ruling-ocr|/ocr/`)
	// The db package's embedded model output: the judge's answers and the
	// model's English titles.
	modelEmbeds = map[string]bool{"Judgements": true, "TitlesEn": true}
)

// Packages whose import means reading model output: the judge's and the
// citer's answers, OCR of judgments, and the model APIs themselves.
var modelImports = []string{
	"github.com/taktekhq/lebanese-legislation/internal/judge",
	"github.com/taktekhq/lebanese-legislation/internal/cite",
	"github.com/taktekhq/lebanese-legislation/cmd/citeagy",
	"github.com/taktekhq/lebanese-legislation/internal/rulingfile",
	"github.com/anthropics/anthropic-sdk-go",
	"google.golang.org/genai",
	"github.com/google/generative-ai-go",
}

// ScanWriters looks through the Go, shell, Python and SQL files under root
// (tests, testdata below root, archive/ and .git left out) for:
//
//   - a write to a law-text column (an INSERT, COPY or UPDATE of its text in
//     an SQL string, a build row added or copied into its table) in any file
//     but WriterFiles;
//   - a package that writes a law-text column and reads model output, in
//     any of its files: it imports the judge, the citer, OCR or a model API,
//     names db/model/*, machine readings or OCR output, or uses the db
//     package's embedded model output. The package is the unit, so a writer
//     file that calls a helper of its own package which reads model output
//     is caught. A script is its own unit;
//   - a package that writes a law-text column and imports a package of this
//     module that reads model output, directly or through others, unless
//     that package is one of DecisionPackages;
//   - a package that reads model output and may write a law-text column by a
//     path the write patterns above do not see: a text table's name passed
//     to any function or method (a helper's copyRows(ctx, conn,
//     "legislation_article", ...)), declared or assigned as a constant
//     (const table = "amendment"), or an UPDATE of a text table whose
//     columns are joined on later ("UPDATE article_version SET " + col).
//     These are not flagged in a package that reads no model output: the
//     export names the text tables to read them;
//   - an exported function or method of a decision package that returns
//     free text.
func ScanWriters(root string) []Finding {
	modPath, modRoot := module(root)
	writers := map[string]bool{}
	for _, w := range WriterFiles {
		writers[w] = true
	}
	decision := map[string]bool{}
	for _, d := range DecisionPackages {
		decision[d] = true
	}
	var out []Finding
	pkgs := map[string]*goPkg{} // by import path (or directory when there is no go.mod)
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "archive", "node_modules", "vendor":
				return filepath.SkipDir
			case "testdata":
				if p != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		switch ext := filepath.Ext(p); {
		case strings.HasSuffix(p, "_test.go"):
		case ext == ".go":
			f := scanGoFile(p)
			if f == nil {
				return nil
			}
			f.rel = rel
			dir := filepath.Dir(p)
			key := importPath(modPath, modRoot, dir)
			pk := pkgs[key]
			if pk == nil {
				pk = &goPkg{path: key, dir: path.Dir(rel)}
				pkgs[key] = pk
			}
			pk.files = append(pk.files, f)
			for _, w := range f.writes {
				if !writers[rel] {
					out = append(out, Finding{rel, w.line, "writes " + w.what + " outside the build's writers"})
					break
				}
			}
		case ext == ".sh" || ext == ".py" || ext == ".sql":
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			writes := sqlWrites(string(raw), 1)
			if len(writes) == 0 {
				return nil
			}
			if !writers[rel] {
				out = append(out, Finding{rel, writes[0].line, "writes " + writes[0].what + " outside the build's writers"})
			}
			if m := modelPath.FindStringIndex(string(raw)); m != nil {
				out = append(out, Finding{rel, writes[0].line, "reads model output (names " + modelPath.FindString(string(raw)) +
					", line " + strconv.Itoa(lineOf(string(raw), m[0])) + ") and writes " + writes[0].what})
			}
		}
		return nil
	})

	// Which packages read model output, themselves or through a package of
	// theirs that is not a decision package.
	why := map[string]string{} // import path -> how it reads model output
	for _, pk := range pkgs {
		for _, f := range pk.files {
			if len(f.reads) > 0 {
				why[pk.path] = fmt.Sprintf("%s:%d %s", f.rel, f.reads[0].line, f.reads[0].what)
				break
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, pk := range sortedPkgs(pkgs) {
			if why[pk.path] != "" {
				continue
			}
			for _, imp := range pk.imports() {
				if w := why[imp]; w != "" && !decision[imp] {
					why[pk.path] = "imports " + imp + ", which reads model output (" + w + ")"
					changed = true
					break
				}
			}
		}
	}
	for _, pk := range sortedPkgs(pkgs) {
		if w := why[pk.path]; w != "" {
			if f, h := pk.firstWrite(); f != nil {
				out = append(out, Finding{f.rel, h.line, "package " + pk.dir + " reads model output (" + w + ") and writes " + h.what})
			} else if f, h := pk.firstTouch(); f != nil {
				out = append(out, Finding{f.rel, h.line, "package " + pk.dir + " reads model output (" + w + ") and may write law text: " + h.what})
			}
		}
		if decision[pk.path] {
			out = append(out, apiText(pk, pkgs, why)...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

type hit struct {
	line int
	what string
}

type goFile struct {
	rel           string
	writes, reads []hit
	touches       []hit // a text table named where a write may follow (ScanWriters)
	imports       []string
	ast           *ast.File
	fset          *token.FileSet
}

type goPkg struct {
	path  string // import path
	dir   string // relative to the root scanned
	files []*goFile
}

func (p *goPkg) imports() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range p.files {
		for _, i := range f.imports {
			if !seen[i] {
				seen[i] = true
				out = append(out, i)
			}
		}
	}
	sort.Strings(out)
	return out
}

// firstWrite is the package's first law-text write, by file name.
func (p *goPkg) firstWrite() (*goFile, hit) {
	fs := append([]*goFile(nil), p.files...)
	sort.Slice(fs, func(i, j int) bool { return fs[i].rel < fs[j].rel })
	for _, f := range fs {
		if len(f.writes) > 0 {
			return f, f.writes[0]
		}
	}
	return nil, hit{}
}

// firstTouch is the package's first text table named where a write may
// follow, by file name.
func (p *goPkg) firstTouch() (*goFile, hit) {
	fs := append([]*goFile(nil), p.files...)
	sort.Slice(fs, func(i, j int) bool { return fs[i].rel < fs[j].rel })
	for _, f := range fs {
		if len(f.touches) > 0 {
			return f, f.touches[0]
		}
	}
	return nil, hit{}
}

func sortedPkgs(m map[string]*goPkg) []*goPkg {
	out := make([]*goPkg, 0, len(m))
	for _, p := range m {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// module finds the go.mod at or above root: the module's path and folder.
func module(root string) (string, string) {
	dir, err := filepath.Abs(root)
	if err != nil {
		return "", ""
	}
	for {
		if f, err := os.Open(filepath.Join(dir, "go.mod")); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if m, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
					f.Close()
					return strings.Trim(strings.TrimSpace(m), `"`), dir
				}
			}
			f.Close()
			return "", ""
		}
		up := filepath.Dir(dir)
		if up == dir {
			return "", ""
		}
		dir = up
	}
}

// importPath is a folder's import path in the module (its absolute path
// when there is no module).
func importPath(modPath, modRoot, dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	if modPath == "" {
		return abs
	}
	rel, err := filepath.Rel(modRoot, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return abs
	}
	if rel == "." {
		return modPath
	}
	return modPath + "/" + filepath.ToSlash(rel)
}

func lineOf(s string, at int) int { return strings.Count(s[:at], "\n") + 1 }

func sqlWrites(s string, line int) []hit {
	var out []hit
	for _, re := range []*regexp.Regexp{sqlInsert, sqlCopy} {
		for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
			out = append(out, hit{line + strings.Count(s[:m[0]], "\n"), s[m[2]:m[3]]})
		}
	}
	for _, m := range sqlUpdate.FindAllStringSubmatchIndex(s, -1) {
		out = append(out, hit{line + strings.Count(s[:m[0]], "\n"), s[m[2]:m[3]] + "." + s[m[4]:m[5]]})
	}
	return out
}

// scanGo finds a Go file's law-text writes and model-output reads.
func scanGo(path string) (writes, reads []hit) {
	f := scanGoFile(path)
	if f == nil {
		return nil, nil
	}
	return f.writes, f.reads
}

func scanGoFile(path string) *goFile {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil
	}
	out := &goFile{ast: f, fset: fset}
	line := func(n ast.Node) int { return fset.Position(n.Pos()).Line }
	dbName, reName := "", ""
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		out.imports = append(out.imports, p)
		for _, m := range modelImports {
			if p == m || strings.HasPrefix(p, m+"/") {
				out.reads = append(out.reads, hit{line(im), "imports " + p})
			}
		}
		if p == "github.com/taktekhq/lebanese-legislation/db" {
			dbName = "db"
			if im.Name != nil {
				dbName = im.Name.Name
			}
		}
		if p == "regexp" {
			reName = "regexp"
			if im.Name != nil {
				reName = im.Name.Name
			}
		}
	}
	// A regular expression's pattern names paths to match them, not to read
	// them: this package's own patterns name db/model/, and the packages
	// that import it (export) read no model output for that.
	pattern := map[ast.Node]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			// "INSERT INTO " + "legislation_article ...": a constant split in two.
			if s, ok := constString(x); ok {
				constSQL(out, s, line(x), pattern[x])
				return false
			}
		case *ast.BasicLit:
			if s, ok := constString(x); ok {
				constSQL(out, s, line(x), pattern[x])
			}
		case *ast.ValueSpec:
			// const table = "legislation_article"
			for _, v := range x.Values {
				if s, ok := constString(v); ok && textTable.MatchString(s) {
					out.touches = append(out.touches, hit{line(v), "names table " + s})
				}
			}
		case *ast.AssignStmt:
			// table := "legislation_article"
			for _, v := range x.Rhs {
				if s, ok := constString(v); ok && textTable.MatchString(s) {
					out.touches = append(out.touches, hit{line(v), "names table " + s})
				}
			}
		case *ast.SelectorExpr:
			// db.Judgements, db.TitlesEn: the model output the db package embeds.
			if id, ok := x.X.(*ast.Ident); ok && dbName != "" && id.Name == dbName && modelEmbeds[x.Sel.Name] {
				out.reads = append(out.reads, hit{line(x), "db." + x.Sel.Name})
			}
		case *ast.CallExpr:
			// The build's rows: r.add("legislation_article", ...), b.flush(r, ..., "article_version").
			// Any other call given a text table's name may write it too: copyRows(ctx, conn, "amendment", ...).
			name := ""
			switch fn := x.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
				if id, ok := fn.X.(*ast.Ident); ok && reName != "" && id.Name == reName && (name == "MustCompile" || name == "Compile") {
					for _, a := range x.Args {
						ast.Inspect(a, func(n ast.Node) bool {
							if _, ok := n.(ast.Expr); ok {
								pattern[n] = true
							}
							return true
						})
					}
				}
			case *ast.Ident:
				name = fn.Name
			}
			for _, a := range x.Args {
				s, ok := constString(a)
				if !ok || !textTable.MatchString(s) {
					continue
				}
				if _, sel := x.Fun.(*ast.SelectorExpr); sel && (name == "add" || name == "flush") {
					out.writes = append(out.writes, hit{line(x), s + " rows"})
				} else {
					if name == "" {
						name = "a call"
					}
					out.touches = append(out.touches, hit{line(x), s + " passed to " + name})
				}
			}
		case *ast.CompositeLit:
			// pgx.Identifier{"article_version"}, as CopyFrom takes it.
			if sel, ok := x.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Identifier" {
				for _, e := range x.Elts {
					if s, ok := constString(e); ok && textTable.MatchString(s) {
						out.writes = append(out.writes, hit{line(x), s + " (copy)"})
					}
				}
			}
		}
		return true
	})
	return out
}

// constSQL notes what a constant string writes or reads: a law-text write,
// a model output's name (unless the string is a regular expression's
// pattern), and an UPDATE of a text table whose columns are not in it.
func constSQL(f *goFile, s string, at int, pattern bool) {
	w := sqlWrites(s, at)
	f.writes = append(f.writes, w...)
	if !pattern && modelPath.MatchString(s) {
		f.reads = append(f.reads, hit{at, "names " + modelPath.FindString(s)})
	}
	if len(w) == 0 {
		if m := sqlUpdateAny.FindStringSubmatchIndex(s); m != nil {
			f.touches = append(f.touches, hit{at + strings.Count(s[:m[0]], "\n"), "UPDATE " + s[m[2]:m[3]] + ", its columns not in the same string"})
		}
	}
}

// apiText checks a decision package's exported functions and methods: none
// may return free text, at any depth of its results (slices, maps,
// pointers, functions, the exported fields of the package's own structs).
// A named type of the package whose underlying type is a string is a
// decision's value (a state, a model's name), not free text.
func apiText(pk *goPkg, pkgs map[string]*goPkg, why map[string]string) []Finding {
	types := map[string]ast.Expr{}
	for _, f := range pk.files {
		for _, d := range f.ast.Decls {
			if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.TYPE {
				for _, s := range g.Specs {
					ts := s.(*ast.TypeSpec)
					types[ts.Name.Name] = ts.Type
				}
			}
		}
	}
	var out []Finding
	for _, f := range pk.files {
		file, fset := f.ast, f.fset
		model := map[string]string{} // import name -> path, for imports that read model output
		for _, im := range file.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			isModel := why[p] != "" && pkgs[p] != pk
			for _, m := range modelImports {
				isModel = isModel || p == m || strings.HasPrefix(p, m+"/")
			}
			if !isModel {
				continue
			}
			name := path.Base(p)
			if im.Name != nil {
				name = im.Name.Name
			}
			model[name] = p
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() || fn.Type.Results == nil {
				continue
			}
			name := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) == 1 {
				recv := fn.Recv.List[0].Type
				if s, ok := recv.(*ast.StarExpr); ok {
					recv = s.X
				}
				id, ok := recv.(*ast.Ident)
				if !ok || !id.IsExported() {
					continue
				}
				name = id.Name + "." + name
			}
			for _, r := range fn.Type.Results.List {
				if what := freeText(r.Type, types, model, map[string]bool{}); what != "" {
					out = append(out, Finding{f.rel, fset.Position(fn.Pos()).Line,
						"decision package " + pk.dir + ": exported " + name + " returns free text (" + what + ")"})
					break
				}
			}
		}
	}
	return out
}

// freeText says what in a result type can carry text, or "".
func freeText(e ast.Expr, types map[string]ast.Expr, model map[string]string, seen map[string]bool) string {
	switch x := e.(type) {
	case *ast.Ident:
		switch x.Name {
		case "string":
			return "string"
		case "any":
			return "any"
		}
		t, ok := types[x.Name]
		if !ok || seen[x.Name] {
			return ""
		}
		if id, ok := t.(*ast.Ident); ok && id.Name == "string" {
			return "" // a named string: a decision's value
		}
		seen[x.Name] = true
		if w := freeText(t, types, model, seen); w != "" {
			return x.Name + ": " + w
		}
	case *ast.ArrayType:
		if id, ok := x.Elt.(*ast.Ident); ok && id.Name == "byte" {
			return "[]byte"
		}
		return freeText(x.Elt, types, model, seen)
	case *ast.MapType:
		if w := freeText(x.Key, types, model, seen); w != "" {
			return w
		}
		return freeText(x.Value, types, model, seen)
	case *ast.StarExpr:
		return freeText(x.X, types, model, seen)
	case *ast.ChanType:
		return freeText(x.Value, types, model, seen)
	case *ast.Ellipsis:
		return freeText(x.Elt, types, model, seen)
	case *ast.InterfaceType:
		return "interface"
	case *ast.FuncType:
		if x.Results != nil {
			for _, r := range x.Results.List {
				if w := freeText(r.Type, types, model, seen); w != "" {
					return w
				}
			}
		}
	case *ast.StructType:
		for _, fl := range x.Fields.List {
			exported := len(fl.Names) == 0 // an embedded field
			for _, n := range fl.Names {
				exported = exported || n.IsExported()
			}
			if !exported {
				continue
			}
			if w := freeText(fl.Type, types, model, seen); w != "" {
				return w
			}
		}
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok && model[id.Name] != "" {
			return model[id.Name] + "." + x.Sel.Name
		}
	case *ast.IndexExpr:
		return freeText(x.X, types, model, seen)
	case *ast.ParenExpr:
		return freeText(x.X, types, model, seen)
	}
	return ""
}

// constString is a string literal, or literals joined with +.
func constString(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		a, ok1 := constString(x.X)
		b, ok2 := constString(x.Y)
		return a + b, ok1 && ok2
	case *ast.ParenExpr:
		return constString(x.X)
	}
	return "", false
}
