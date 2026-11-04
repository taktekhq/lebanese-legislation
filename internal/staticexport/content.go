package staticexport

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template/parse"

	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

// Content changes are the site's templates, UI strings, topic guides and
// assets. A change to Go code still renders everything; these four are mapped
// to the pages they touch:
//
//   - a page template: the routes whose pages executed a page template that
//     reaches it (the record each render leaves, Deps, says which routes
//     executed which page template; the templates themselves say what reaches
//     what);
//   - a UI string: the routes whose Go code looked it up (Deps), and the
//     routes whose page templates use it, directly or through a template
//     function;
//   - a topic guide: its own pages, the topic index and the article pages it
//     quotes, before and after (every topic page and the index when a title,
//     a question or the set of guides changes);
//   - an asset: no page; every run uploads the assets.
//
// Anything it cannot place renders the whole site, and says why.

const (
	templateDir = "internal/site/templates/"
	i18nDir     = "internal/site/i18n/"
	topicsDir   = "internal/site/topics/"
	staticDir   = "internal/site/static/"
)

// contentFile says whether a changed source is one the content planner maps.
func contentFile(p string) bool {
	switch {
	case strings.HasPrefix(p, templateDir):
		return strings.HasSuffix(p, ".html") && p != layoutFile && !strings.Contains(p[len(templateDir):], "/")
	case strings.HasPrefix(p, i18nDir):
		return strings.HasSuffix(p, ".json") && !strings.Contains(p[len(i18nDir):], "/")
	case strings.HasPrefix(p, topicsDir):
		return strings.HasSuffix(p, ".json") && !strings.Contains(p[len(topicsDir):], "/")
	case strings.HasPrefix(p, staticDir):
		return true
	}
	return false
}

// ContentInput is what PlanContent reads beside the repository.
type ContentInput struct {
	Dir, Since string
	Files      []string         // changed content files (ChangePlan.Content)
	Deps       Deps             // every shard's record, merged
	Manifest   map[string]Entry // every shard's manifest, merged
	Route      func(path string) string
}

// ContentPlan is what content changes need.
type ContentPlan struct {
	Full   bool
	Paths  []string
	Assets bool     // an asset changed: a run must upload the assets
	Why    []string // one line per decision, for the run's summary
}

// PlanContent maps changed content files to pages.
func PlanContent(ctx context.Context, in ContentInput) (ContentPlan, error) {
	var p ContentPlan
	full := func(why string) (ContentPlan, error) {
		return ContentPlan{Full: true, Why: append(p.Why, why)}, nil
	}
	if len(in.Deps) == 0 || len(in.Manifest) == 0 {
		return full("no record yet of what each route's pages use: render everything once")
	}
	var tmplFiles, i18nFiles, topicFiles []string
	for _, f := range in.Files {
		switch {
		case strings.HasPrefix(f, staticDir):
			p.Assets = true
		case strings.HasPrefix(f, templateDir):
			tmplFiles = append(tmplFiles, f)
		case strings.HasPrefix(f, i18nDir):
			i18nFiles = append(i18nFiles, f)
		case strings.HasPrefix(f, topicsDir):
			topicFiles = append(topicFiles, f)
		default:
			return full(f + " is not a content file")
		}
	}
	if p.Assets {
		p.Why = append(p.Why, "assets changed: uploaded by the run, no page renders")
	}
	names := map[string]bool{} // "set:law", "key:nav.home": what an affected page used

	if len(tmplFiles) > 0 || len(i18nFiles) > 0 {
		sets, err := readTemplateSets(ctx, in.Dir, in.Since, tmplFiles)
		if err != nil {
			return full("templates: " + err.Error())
		}
		for _, f := range tmplFiles {
			name := path.Base(f)
			var hit []string
			for set, s := range sets {
				if set != layoutSet && s.files[name] {
					names["set:"+set] = true
					hit = append(hit, set)
				}
			}
			sort.Strings(hit)
			p.Why = append(p.Why, fmt.Sprintf("%s: page templates %s", name, strings.Join(hit, ", ")))
		}
		if len(i18nFiles) > 0 {
			keys, err := changedKeys(ctx, in.Dir, in.Since, i18nFiles)
			if err != nil {
				return full("strings: " + err.Error())
			}
			funcs, err := templateFuncKeys(filepath.Join(in.Dir, "internal/site"), filepath.Join(in.Dir, i18nDir))
			if err != nil {
				return full("template functions: " + err.Error())
			}
			for _, k := range keys {
				if s := sets[layoutSet]; s != nil && s.usesKey(k, funcs) {
					return full("string " + k + " is in the shared layout")
				}
				var users []string
				for set, s := range sets {
					if set != layoutSet && s.usesKey(k, funcs) {
						names["set:"+set] = true
						users = append(users, set)
					}
				}
				names["key:"+k] = true
				sort.Strings(users)
				p.Why = append(p.Why, fmt.Sprintf("string %s: page templates [%s] and the routes that looked it up", k, strings.Join(users, " ")))
			}
		}
	}
	if len(topicFiles) > 0 {
		paths, why, err := topicPaths(ctx, in.Dir, in.Since, topicFiles, in.Manifest)
		if err != nil {
			return full("topics: " + err.Error())
		}
		p.Paths = append(p.Paths, paths...)
		p.Why = append(p.Why, why...)
	}

	if len(names) > 0 {
		groups := map[string]bool{}
		for g := range in.Deps {
			if in.Deps.Uses(g, names) {
				groups[g] = true
			}
		}
		n := 0
		for pth := range in.Manifest {
			if groups[in.Route(pth)] {
				p.Paths = append(p.Paths, pth)
				n++
			}
		}
		gs := make([]string, 0, len(groups))
		for g := range groups {
			gs = append(gs, g)
		}
		sort.Strings(gs)
		p.Why = append(p.Why, fmt.Sprintf("%d pages on %d routes: %s", n, len(gs), strings.Join(gs, "; ")))
	}
	p.Paths = uniqueSorted(p.Paths)
	if len(p.Paths) > len(in.Manifest)/2 {
		return full(fmt.Sprintf("%d of %d pages: render everything", len(p.Paths), len(in.Manifest)))
	}
	return p, nil
}

func uniqueSorted(ps []string) []string {
	sort.Strings(ps)
	out := ps[:0]
	for i, s := range ps {
		if i == 0 || s != ps[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// gitShow is a file at a commit; ok is false when it does not exist there.
func gitShow(ctx context.Context, dir, rev, file string) (b []byte, ok bool, err error) {
	if _, err := gitOutput(ctx, dir, "cat-file", "-e", rev+":"+file); err != nil {
		return nil, false, nil
	}
	b, err = gitOutput(ctx, dir, "show", rev+":"+file)
	return b, err == nil, err
}

// --- templates -------------------------------------------------------------

// layoutSet names the layout's own trees in readTemplateSets' result.
const layoutSet = "layout.html"

// templateRoots are the templates Go executes by name: the layout for every
// page, and the few blocks handlers render on their own.
var templateRoots = []string{"layout.html", "article", "ai_answer", "answer"}

// tmplSet is what a page template reaches from the roots: the files that
// define those templates, the keys its t calls name, the patterns of keys
// built with printf, and the template functions it calls.
type tmplSet struct {
	files    map[string]bool
	keys     map[string]bool
	patterns [][2]string // prefix, suffix
	anyKey   bool        // a t call whose key cannot be read
	funcs    map[string]bool
}

func (s *tmplSet) usesKey(k string, funcKeys map[string]*keyUse) bool {
	if s.anyKey || s.keys[k] {
		return true
	}
	for _, pt := range s.patterns {
		if matchPattern(k, pt) {
			return true
		}
	}
	for f := range s.funcs {
		if u := funcKeys[f]; u != nil && u.matches(k) {
			return true
		}
	}
	return false
}

func matchPattern(k string, pt [2]string) bool {
	return len(k) >= len(pt[0])+len(pt[1]) && strings.HasPrefix(k, pt[0]) && strings.HasSuffix(k, pt[1])
}

// readTemplateSets parses internal/site/templates at HEAD, and the changed
// files at since as well, and returns each page template's set (by its name,
// "law" for law.html) with the files both versions reach, plus the layout's
// own trees under layoutSet.
func readTemplateSets(ctx context.Context, dir, since string, changed []string) (map[string]*tmplSet, error) {
	files, err := filepath.Glob(filepath.Join(dir, templateDir, "*.html"))
	if err != nil {
		return nil, err
	}
	cur := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		cur[filepath.Base(f)] = string(b)
	}
	old := map[string]string{}
	for n, t := range cur {
		old[n] = t
	}
	for _, f := range changed {
		b, ok, err := gitShow(ctx, dir, since, f)
		if err != nil {
			return nil, err
		}
		if ok {
			old[path.Base(f)] = string(b)
		} else {
			delete(old, path.Base(f))
		}
	}
	sets := map[string]*tmplSet{}
	for _, version := range []map[string]string{cur, old} {
		var pages, partials []string
		for n := range version {
			switch {
			case n == "layout.html":
			case strings.HasPrefix(n, "_"):
				partials = append(partials, n)
			default:
				pages = append(pages, n)
			}
		}
		if _, ok := version["layout.html"]; !ok {
			return nil, fmt.Errorf("no layout.html")
		}
		pages = append(pages, layoutSet)
		for _, pg := range pages {
			members := append([]string{"layout.html"}, partials...)
			if pg != layoutSet {
				members = append(members, pg)
			}
			defs := map[string][]*parse.Tree{}
			owner := map[*parse.Tree]string{}
			for _, m := range members {
				trees, err := parseTemplate(m, version[m])
				if err != nil {
					return nil, fmt.Errorf("%s: %w", m, err)
				}
				for name, t := range trees {
					defs[name] = append(defs[name], t)
					owner[t] = m
				}
			}
			roots := templateRoots
			if pg == layoutSet {
				roots = []string{"layout.html"} // the frame alone, without the page's blocks
			}
			name := strings.TrimSuffix(pg, ".html")
			if pg == layoutSet {
				name = layoutSet
			}
			s := sets[name]
			if s == nil {
				s = &tmplSet{files: map[string]bool{}, keys: map[string]bool{}, funcs: map[string]bool{}}
				sets[name] = s
			}
			s.reach(defs, owner, roots, pg == layoutSet)
		}
	}
	return sets, nil
}

func parseTemplate(name, text string) (map[string]*parse.Tree, error) {
	trees := map[string]*parse.Tree{}
	t := parse.New(name)
	t.Mode = parse.SkipFuncCheck
	if _, err := t.Parse(text, "", "", trees); err != nil {
		return nil, err
	}
	return trees, nil
}

// reach walks the templates from roots. For the layout alone, a template the
// pages define (the holes) is not followed.
func (s *tmplSet) reach(defs map[string][]*parse.Tree, owner map[*parse.Tree]string, roots []string, frameOnly bool) {
	seen := map[string]bool{}
	var walk func(n parse.Node)
	visit := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		for _, t := range defs[name] {
			if frameOnly && owner[t] != "layout.html" {
				continue
			}
			s.files[owner[t]] = true
			if t.Root != nil {
				walk(t.Root)
			}
		}
	}
	walk = func(n parse.Node) {
		switch n := n.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, c := range n.Nodes {
				walk(c)
			}
		case *parse.ActionNode:
			walk(n.Pipe)
		case *parse.PipeNode:
			if n == nil {
				return
			}
			for _, c := range n.Cmds {
				walk(c)
			}
		case *parse.CommandNode:
			if len(n.Args) > 0 {
				if id, ok := n.Args[0].(*parse.IdentifierNode); ok && id.Ident == "t" {
					s.tKey(n.Args[1:])
				}
			}
			for _, a := range n.Args {
				walk(a)
			}
		case *parse.IdentifierNode:
			s.funcs[n.Ident] = true
		case *parse.ChainNode:
			walk(n.Node)
		case *parse.IfNode:
			walk(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.RangeNode:
			walk(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.WithNode:
			walk(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.TemplateNode:
			walk(n.Pipe)
			visit(n.Name)
		}
	}
	for _, r := range roots {
		visit(r)
	}
}

// tKey reads the key of a t call: a string, or printf with a format string.
func (s *tmplSet) tKey(args []parse.Node) {
	if len(args) == 0 {
		s.anyKey = true
		return
	}
	switch a := args[0].(type) {
	case *parse.StringNode:
		s.keys[a.Text] = true
		return
	case *parse.PipeNode:
		if len(a.Cmds) == 1 && len(a.Cmds[0].Args) >= 2 {
			if id, ok := a.Cmds[0].Args[0].(*parse.IdentifierNode); ok && id.Ident == "printf" {
				if f, ok := a.Cmds[0].Args[1].(*parse.StringNode); ok {
					if pt, ok := formatPattern(f.Text); ok {
						s.patterns = append(s.patterns, pt)
						return
					}
				}
			}
		}
	}
	s.anyKey = true
}

// formatPattern is the fixed start and end of the keys a format string
// builds ("datefix.%s" is datefix. and nothing); false when both are empty.
func formatPattern(f string) ([2]string, bool) {
	i := strings.IndexByte(f, '%')
	if i < 0 {
		return [2]string{f, ""}, f != ""
	}
	j := strings.LastIndexByte(f, '%')
	suffix := ""
	if j+2 <= len(f) {
		suffix = f[j+2:]
	}
	return [2]string{f[:i], suffix}, f[:i] != "" || suffix != ""
}

// --- template functions ----------------------------------------------------

// keyUse is the strings a Go function body may look up: its string literals
// and the fixed parts of keys it builds ("verb." + x, x + ".desc", Sprintf).
type keyUse struct {
	lits     map[string]bool
	patterns [][2]string
}

func (u *keyUse) matches(k string) bool {
	if u.lits[k] {
		return true
	}
	for _, pt := range u.patterns {
		if matchPattern(k, pt) {
			return true
		}
	}
	return false
}

// templateFuncKeys reads the site's Go code for its template functions (the
// FuncMap entries) and returns, for each, the strings its body and the
// functions it calls in dirs (the site and its string tables) may look up.
func templateFuncKeys(dirs ...string) (map[string]*keyUse, error) {
	var files []string
	for _, d := range dirs {
		fs, err := filepath.Glob(filepath.Join(d, "*.go"))
		if err != nil {
			return nil, err
		}
		files = append(files, fs...)
	}
	fset := token.NewFileSet()
	decls := map[string][]*ast.FuncDecl{} // by name: functions and methods alike
	type entry struct {
		name string
		expr ast.Expr
	}
	var entries []entry
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			return nil, err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				decls[n.Name.Name] = append(decls[n.Name.Name], n)
			case *ast.CompositeLit:
				if isFuncMap(n.Type) {
					for _, el := range n.Elts {
						if kv, ok := el.(*ast.KeyValueExpr); ok {
							if k, ok := stringLit(kv.Key); ok {
								entries = append(entries, entry{k, kv.Value})
							}
						}
					}
				}
			case *ast.AssignStmt:
				for i, l := range n.Lhs {
					ix, ok := l.(*ast.IndexExpr)
					if !ok || i >= len(n.Rhs) {
						continue
					}
					if _, isLit := n.Rhs[i].(*ast.FuncLit); !isLit {
						continue
					}
					if k, ok := stringLit(ix.Index); ok {
						entries = append(entries, entry{k, n.Rhs[i]})
					}
				}
			}
			return true
		})
	}
	out := map[string]*keyUse{}
	for _, e := range entries {
		u := out[e.name]
		if u == nil {
			u = &keyUse{lits: map[string]bool{}}
			out[e.name] = u
		}
		seen := map[*ast.FuncDecl]bool{}
		var collect func(n ast.Node)
		// follow reads the functions a name may call: functions and methods
		// alike, by name. Only called names are followed; a bare name in a
		// body is usually a variable (funcs["static"] returns one).
		follow := func(name string) {
			for _, d := range decls[name] {
				if !seen[d] && d.Body != nil {
					seen[d] = true
					collect(d.Body)
				}
			}
		}
		collect = func(n ast.Node) {
			ast.Inspect(n, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BasicLit:
					if s, ok := stringLit(n); ok {
						u.lits[s] = true
					}
				case *ast.BinaryExpr:
					if n.Op == token.ADD {
						if s, ok := stringLit(n.X); ok && s != "" {
							u.patterns = append(u.patterns, [2]string{s, ""})
						}
						if s, ok := stringLit(n.Y); ok && s != "" {
							u.patterns = append(u.patterns, [2]string{"", s})
						}
					}
				case *ast.CallExpr:
					if len(n.Args) > 0 {
						if s, ok := stringLit(n.Args[0]); ok && strings.Contains(s, "%") {
							if pt, ok := formatPattern(s); ok {
								u.patterns = append(u.patterns, pt)
							}
						}
					}
					switch f := n.Fun.(type) {
					case *ast.Ident:
						follow(f.Name)
					case *ast.SelectorExpr:
						follow(f.Sel.Name)
					}
				}
				return true
			})
		}
		switch v := e.expr.(type) { // "lawURL": lawURL names the function itself
		case *ast.Ident:
			follow(v.Name)
		case *ast.SelectorExpr:
			follow(v.Sel.Name)
		default:
			collect(v)
		}
	}
	return out, nil
}

func isFuncMap(t ast.Expr) bool {
	switch t := t.(type) {
	case *ast.SelectorExpr:
		return t.Sel.Name == "FuncMap"
	case *ast.Ident:
		return t.Name == "FuncMap"
	}
	return false
}

func stringLit(e ast.Expr) (string, bool) {
	b, ok := e.(*ast.BasicLit)
	if !ok || b.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(b.Value)
	return s, err == nil
}

// --- strings ---------------------------------------------------------------

// changedKeys are the keys added, removed or changed in the string tables.
func changedKeys(ctx context.Context, dir, since string, files []string) ([]string, error) {
	keys := map[string]bool{}
	for _, f := range files {
		load := func(b []byte, ok bool) (map[string]string, error) {
			m := map[string]string{}
			if !ok {
				return m, nil
			}
			return m, json.Unmarshal(b, &m)
		}
		ob, ook, err := gitShow(ctx, dir, since, f)
		if err != nil {
			return nil, err
		}
		nb, err := os.ReadFile(filepath.Join(dir, f))
		nok := err == nil
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		om, err := load(ob, ook)
		if err != nil {
			return nil, fmt.Errorf("%s at %s: %w", f, since, err)
		}
		nm, err := load(nb, nok)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for k, v := range nm {
			if ov, ok := om[k]; !ok || ov != v {
				keys[k] = true
			}
		}
		for k := range om {
			if _, ok := nm[k]; !ok {
				keys[k] = true
			}
		}
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// --- topic guides ----------------------------------------------------------

// topicPaths are the pages changed topic guides touch.
func topicPaths(ctx context.Context, dir, since string, files []string, manifest map[string]Entry) ([]string, []string, error) {
	var paths, why []string
	add := func(p string) { paths = append(paths, p, "/en"+p) }
	listed := false // a title, a question or the set of guides changed: every topic page lists them all
	for _, f := range files {
		read := func(b []byte, ok bool) (*topics.Topic, error) {
			if !ok {
				return nil, nil
			}
			var t topics.Topic
			return &t, json.Unmarshal(b, &t)
		}
		ob, ook, err := gitShow(ctx, dir, since, f)
		if err != nil {
			return nil, nil, err
		}
		nb, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
		nok := err == nil
		o, err := read(ob, ook)
		if err != nil {
			return nil, nil, fmt.Errorf("%s at %s: %w", f, since, err)
		}
		n, err := read(nb, nok)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f, err)
		}
		heading := func(t *topics.Topic) string {
			if t == nil {
				return ""
			}
			return t.Slug + "\x00" + t.EN.Title + "\x00" + t.EN.Question + "\x00" + t.AR.Title + "\x00" + t.AR.Question
		}
		cites := func(t *topics.Topic) map[[2]string]bool {
			m := map[[2]string]bool{}
			if t != nil {
				for _, c := range t.Articles {
					m[[2]string{strconv.Itoa(c.Law), c.Article}] = true
				}
			}
			return m
		}
		oc, nc := cites(o), cites(n)
		touched := map[[2]string]bool{} // the article pages whose list of guides changes
		renamed := heading(o) != heading(n)
		for c := range oc {
			if renamed || !nc[c] {
				touched[c] = true
			}
		}
		for c := range nc {
			if renamed || !oc[c] {
				touched[c] = true
			}
		}
		listed = listed || renamed
		for _, t := range []*topics.Topic{o, n} {
			if t != nil {
				add("/topic/" + t.Slug + "/")
			}
		}
		arts := 0
		for c := range touched {
			slug := url.PathEscape(strings.Join(strings.Fields(c[1]), "-"))
			for _, word := range []string{"law", "decree", "text"} {
				for _, pre := range []string{"", "/en"} {
					p := pre + "/" + word + "/" + c[0] + "/article/" + slug + "/"
					if _, ok := manifest[p]; ok {
						paths = append(paths, p)
						arts++
					}
				}
			}
		}
		why = append(why, fmt.Sprintf("%s: its pages and %d article pages it quotes or stopped quoting", path.Base(f), arts))
	}
	if listed {
		all, err := filepath.Glob(filepath.Join(dir, topicsDir, "*.json"))
		if err != nil {
			return nil, nil, err
		}
		for _, f := range all {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, nil, err
			}
			var t struct{ Slug string }
			if err := json.Unmarshal(b, &t); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", f, err)
			}
			if t.Slug != "" {
				add("/topic/" + t.Slug + "/")
			}
		}
		add("/topics/")
		add("/")
		why = append(why, "a guide's title, question or the set of guides changed: every topic page, the topic index and the home pages")
	}
	return paths, why, nil
}
