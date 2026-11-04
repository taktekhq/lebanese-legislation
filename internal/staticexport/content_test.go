package staticexport

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A content fixture: a layout, a partial only the law page uses, two page
// templates, the string tables, a template function, and two topic guides.
func contentFixture(t *testing.T) (dir string, commit func(msg string) string, write func(name, body string)) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
		return strings.TrimSpace(string(b))
	}
	write = func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit = func(msg string) string {
		git("add", "-A")
		git("commit", "-qm", msg)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q")
	git("config", "user.email", "fixture@example.org")
	git("config", "user.name", "Fixture")
	write(layoutFile, `<html><body><nav>{{t "nav.home"}}</nav>{{template "content" .}}</body></html>`)
	write(templateDir+"_lawbits.html", `{{define "lawbits"}}<p>{{t "law.bits"}} {{statusText .S}}</p>{{end}}`)
	write(templateDir+"law.html", `{{define "content"}}<h1>{{t "law.title"}}</h1>{{template "lawbits" .}}{{t (printf "datefix.%s" .M)}}{{end}}`)
	write(templateDir+"about.html", `{{define "content"}}<h1>{{t "about.title"}}</h1>{{end}}`)
	write("internal/site/funcs.go", `package site

import "html/template"

var baseFuncs = template.FuncMap{
	"statusText": func(s string) string {
		if s == "repealed" {
			return enBundle.T("status.repealed")
		}
		return enBundle.T("status.in_force")
	},
}
`)
	write(i18nDir+"en.json", `{"nav.home":"Home","law.title":"Law","law.bits":"Bits","about.title":"About","status.repealed":"Repealed","status.in_force":"In force","datefix.a":"A","privacy.title":"Privacy","unused.key":"x"}`)
	write(i18nDir+"ar.json", `{"nav.home":"الرئيسية"}`)
	write(topicsDir+"inheritance.json", `{"slug":"inheritance","articles":[{"id":"a","law":7,"article":"1"},{"id":"b","law":7,"article":"2"}],"en":{"title":"Inheritance","question":"Who inherits?"},"ar":{"title":"الإرث","question":"من يرث؟"}}`)
	write(topicsDir+"rent.json", `{"slug":"rent","articles":[{"id":"a","law":8,"article":"5"}],"en":{"title":"Rent","question":"Rent?"},"ar":{"title":"الإيجار","question":"؟"}}`)
	write(staticDir+"styles.css", "body{}")
	return dir, commit, write
}

func contentInput(dir, since string, files []string) ContentInput {
	route := func(p string) string {
		p = strings.TrimPrefix(p, "/en")
		switch {
		case strings.Contains(p, "/article/"):
			return "GET /law/{id}/article/{n}/{$}"
		case strings.HasPrefix(p, "/law/"):
			return "GET /law/{id}/{$}"
		case strings.HasPrefix(p, "/topic/"):
			return "GET /topic/{slug}/{$}"
		}
		return "GET " + p + "{$}"
	}
	manifest := map[string]Entry{}
	for _, p := range []string{"/law/7/", "/law/7/article/1/", "/law/7/article/2/", "/law/7/article/3/", "/law/8/article/5/",
		"/about/", "/privacy/", "/topic/inheritance/", "/topic/rent/", "/topics/", "/"} {
		manifest[p] = Entry{Path: p, Status: http.StatusOK}
		manifest["/en"+p] = Entry{Path: "/en" + p, Status: http.StatusOK}
	}
	deps := Deps{
		"GET /law/{id}/{$}":             {"key:brand.name", "set:law"},
		"GET /law/{id}/article/{n}/{$}": {"key:brand.name", "set:law"},
		"GET /about/{$}":                {"key:brand.name", "set:about"},
		"GET /privacy/{$}":              {"key:brand.name", "key:privacy.title", "set:privacy"},
		"GET /topic/{slug}/{$}":         {"set:topic"},
		"GET /topics/{$}":               {"set:topics"},
		"GET /{$}":                      {"set:home"},
	}
	return ContentInput{Dir: dir, Since: since, Files: files, Deps: deps, Manifest: manifest, Route: route}
}

func TestPlanContent(t *testing.T) {
	dir, commit, write := contentFixture(t)
	base := commit("base")
	plan := func(files ...string) ContentPlan {
		t.Helper()
		p, err := PlanContent(context.Background(), contentInput(dir, base, files))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	want := func(name string, p ContentPlan, paths ...string) {
		t.Helper()
		if p.Full {
			t.Fatalf("%s: full render: %v", name, p.Why)
		}
		var exp []string
		for _, q := range paths {
			exp = append(exp, q, "/en"+q)
		}
		exp = uniqueSorted(exp)
		if strings.Join(p.Paths, " ") != strings.Join(exp, " ") {
			t.Fatalf("%s:\n got  %v\n want %v\n why  %v", name, p.Paths, exp, p.Why)
		}
	}
	reset := func() {
		t.Helper()
		c := exec.Command("git", "reset", "-q", "--hard", base)
		c.Dir = dir
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, b)
		}
	}

	write(templateDir+"about.html", `{{define "content"}}<h1>{{t "about.title"}}!</h1>{{end}}`)
	commit("about")
	want("a page template", plan(templateDir+"about.html"), "/about/")
	reset()

	write(templateDir+"_lawbits.html", `{{define "lawbits"}}<p>{{t "law.bits"}}!</p>{{end}}`)
	commit("partial")
	want("a partial only the law template reaches", plan(templateDir+"_lawbits.html"),
		"/law/7/", "/law/7/article/1/", "/law/7/article/2/", "/law/7/article/3/", "/law/8/article/5/")
	reset()

	write(i18nDir+"en.json", `{"nav.home":"Home","law.title":"Law","law.bits":"Bits","about.title":"About!","status.repealed":"Repealed","status.in_force":"In force","datefix.a":"A","privacy.title":"Privacy!","unused.key":"y"}`)
	commit("strings")
	want("strings in a template, in Go code, and unused", plan(i18nDir+"en.json"), "/about/", "/privacy/")
	reset()

	write(i18nDir+"en.json", `{"nav.home":"Home","law.title":"Law","law.bits":"Bits","about.title":"About","status.repealed":"Gone","status.in_force":"In force","datefix.a":"A!","privacy.title":"Privacy","unused.key":"x"}`)
	commit("function and printf strings")
	want("a string a template function and a printf key look up", plan(i18nDir+"en.json"),
		"/law/7/", "/law/7/article/1/", "/law/7/article/2/", "/law/7/article/3/", "/law/8/article/5/")
	reset()

	write(i18nDir+"ar.json", `{"nav.home":"البداية"}`)
	commit("layout string")
	if p := plan(i18nDir + "ar.json"); !p.Full {
		t.Fatalf("a string in the shared layout must render everything: %+v", p)
	}
	reset()

	write(topicsDir+"inheritance.json", `{"slug":"inheritance","articles":[{"id":"a","law":7,"article":"1"},{"id":"c","law":7,"article":"3"}],"en":{"title":"Inheritance","question":"Who inherits?"},"ar":{"title":"الإرث","question":"من يرث؟"}}`)
	commit("cites")
	want("a guide's quotes change", plan(topicsDir+"inheritance.json"),
		"/topic/inheritance/", "/law/7/article/2/", "/law/7/article/3/")
	reset()

	write(topicsDir+"rent.json", `{"slug":"rent","articles":[{"id":"a","law":8,"article":"5"}],"en":{"title":"Renting","question":"Rent?"},"ar":{"title":"الإيجار","question":"؟"}}`)
	commit("title")
	want("a guide's title changes", plan(topicsDir+"rent.json"),
		"/topic/rent/", "/topic/inheritance/", "/topics/", "/", "/law/8/article/5/")
	reset()

	write(templateDir+"_shared.html", `{{define "shared"}}<aside></aside>{{end}}`)
	write(templateDir+"law.html", `{{define "content"}}<h1>{{t "law.title"}}</h1>{{template "lawbits" .}}{{template "shared"}}{{end}}`)
	write(templateDir+"about.html", `{{define "content"}}<h1>{{t "about.title"}}</h1>{{template "shared"}}{{end}}`)
	shared := commit("shared partial")
	write(templateDir+"_shared.html", `{{define "shared"}}<aside>!</aside>{{end}}`)
	commit("shared change")
	in := contentInput(dir, shared, []string{templateDir + "_shared.html"})
	if p, err := PlanContent(context.Background(), in); err != nil || !p.Full {
		t.Fatalf("a partial most pages reach must render everything: %+v %v", p, err)
	}
	reset()

	write(staticDir+"styles.css", "body{margin:0}")
	commit("css")
	if p := plan(staticDir + "styles.css"); p.Full || !p.Assets || len(p.Paths) != 0 {
		t.Fatalf("an asset: %+v", p)
	}
	reset()

	in = contentInput(dir, base, []string{templateDir + "about.html"})
	in.Deps = nil
	if p, err := PlanContent(context.Background(), in); err != nil || !p.Full {
		t.Fatalf("no record: %+v %v", p, err)
	}
}

// The planner hands content files over instead of rendering everything, and
// a Go change still renders everything.
func TestPlanChangesContent(t *testing.T) {
	dir, commit, write := contentFixture(t)
	base := commit("base")
	write(templateDir+"about.html", `{{define "content"}}x{{end}}`)
	write(topicsDir+"rent.json", `{"slug":"rent"}`)
	commit("content")
	p, err := PlanChanges(context.Background(), dir, base, false)
	if err != nil || p.Full || !slices.Equal(p.Content, []string{templateDir + "about.html", topicsDir + "rent.json"}) {
		t.Fatalf("%+v %v", p, err)
	}
	write("internal/site/funcs.go", "package site\n")
	commit("go")
	if p, err := PlanChanges(context.Background(), dir, base, false); err != nil || !p.Full {
		t.Fatalf("a Go change: %+v %v", p, err)
	}
}

// The real templates and template functions parse, and the facts the planner
// leans on hold: every page reaches its own file, the law page reaches the
// shared blocks, and plural's strings are found inside the i18n package.
func TestContentRealSite(t *testing.T) {
	root := filepath.Join("..", "..")
	sets, err := readTemplateSets(context.Background(), root, "HEAD", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) < 20 {
		t.Fatalf("only %d page templates", len(sets))
	}
	for name, s := range sets {
		if name != layoutSet && !s.files[name+".html"] {
			t.Errorf("%s does not reach %s.html", name, name)
		}
		if !s.files["layout.html"] {
			t.Errorf("%s does not reach layout.html", name)
		}
	}
	if !sets["law"].keys["back.home"] && len(sets["law"].keys) < 10 {
		t.Errorf("law reads few strings: %d", len(sets["law"].keys))
	}
	funcs, err := templateFuncKeys(filepath.Join(root, "internal/site"), filepath.Join(root, i18nDir))
	if err != nil {
		t.Fatal(err)
	}
	for f, k := range map[string]string{"plural": "plural.article.few", "statusText": "status.repealed", "linkVerb": "verb.amends"} {
		if u := funcs[f]; u == nil || !u.matches(k) {
			t.Errorf("template function %s: %s not found", f, k)
		}
	}
}

// A run records what each route's pages noted, and a run of listed paths keeps
// the record of the routes it did not visit.
func TestRunRecordsDeps(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n, ok := r.Context().Value(testDepsKey{}).(*[]string); ok {
			*n = append(*n, "set:"+strings.Trim(r.URL.Path, "/"))
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(r.URL.Path))
	})
	record := func(r *http.Request) (*http.Request, func() []string) {
		var names []string
		return r.WithContext(contextWith(r, &names)), func() []string { return names }
	}
	out := t.TempDir()
	o := Options{Out: out, Paths: []string{"/a/", "/b/"}, Record: record, Group: func(p string) string { return "g" + p },
		PrevDeps: Deps{"g/old/": {"set:old"}, "g/a/": {"set:stale"}}}
	if _, err := Run(context.Background(), h, o); err != nil {
		t.Fatal(err)
	}
	d, err := ReadDeps(filepath.Join(out, ".export", "deps.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(d["g/a/"], " ") != "set:a set:stale" || strings.Join(d["g/b/"], " ") != "set:b" || strings.Join(d["g/old/"], " ") != "set:old" {
		t.Fatalf("listed run: %v", d)
	}
}

type testDepsKey struct{}

func contextWith(r *http.Request, names *[]string) context.Context {
	return context.WithValue(r.Context(), testDepsKey{}, names)
}
