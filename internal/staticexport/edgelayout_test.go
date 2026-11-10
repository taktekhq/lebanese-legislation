package staticexport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	lsite "github.com/taktekhq/lebanese-legislation/internal/site"
)

// edgeSite is the real site with no database: the pages that need none.
// The command sets this; the tests use the same reader.
func init() { LayoutSchemas = lsite.LayoutSchemas }

func edgeSite(t *testing.T) (*lsite.Server, *Layout) {
	t.Helper()
	s, err := lsite.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.SetAnalytics(true)
	frames, err := s.LayoutFrames()
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewLayout(frames)
	if err != nil {
		t.Fatal(err)
	}
	return s, l
}

var edgePaths = []string{"/blog/", "/en/blog/", "/ai/", "/en/ai/", "/contribute/", "/en/contribute/", "/run-it-yourself/", "/en/run-it-yourself/"}

// A page in parts, joined back into its frame, is the page byte for byte;
// the parts hold no piece of the shared header or footer.
func TestSplitJoinRealPages(t *testing.T) {
	s, l := edgeSite(t)
	for _, p := range edgePaths {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		body := w.Body.Bytes()
		obj, key, ok := l.Split(body)
		if !ok {
			t.Fatalf("%s: not split", p)
		}
		lang := "ar"
		if strings.HasPrefix(p, "/en/") {
			lang = "en"
		}
		if !strings.HasPrefix(key, lang+"/") {
			t.Errorf("%s: frame %s", p, key)
		}
		back, err := l.Join(obj)
		if err != nil || !bytes.Equal(back, body) {
			t.Fatalf("%s: joined page differs (%v)", p, err)
		}
		for _, shared := range []string{`<footer class="footer">`, `src="/ga.js?v=`, `<nav class="nav__links"`, "@font-face"} {
			if bytes.Contains(obj, []byte(shared)) {
				t.Errorf("%s: the parts hold the shared %s", p, shared)
			}
		}
	}
}

// Pages outside the frame, and pages a Transform changed in the frame (the
// search page's script before </body>), are stored whole.
func TestSplitLeavesOtherPagesWhole(t *testing.T) {
	s, l := edgeSite(t)
	for _, b := range []string{`<html><body>x</body></html>`, `<?xml version="1.0"?><urlset/>`, ``} {
		if _, _, ok := l.Split([]byte(b)); ok {
			t.Errorf("%q was split", b)
		}
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/blog/", nil))
	tr := SearchShell("")("/search/", "text/html", w.Body.Bytes())
	if _, _, ok := l.Split(tr); ok {
		t.Error("a page with a script added to the frame was split")
	}
}

// With a Layout the manifest hash covers only a page's parts: a run with a
// new footer (another frame, same schema) uploads no page.
func TestLayoutChangeUploadsNoPage(t *testing.T) {
	s, l := edgeSite(t)
	out := t.TempDir()
	sent := map[string]int{}
	o := Options{Out: out, Paths: edgePaths, Layout: l, Sink: func(e Entry, b []byte) error {
		if !bytes.HasPrefix(b, []byte(PartsMagic)) {
			t.Errorf("%s stored whole", e.Path)
		}
		sent[e.Path]++
		return nil
	}}
	st, err := Run(context.Background(), s, o)
	if err != nil {
		t.Fatal(err)
	}
	if st.Written != int64(len(edgePaths)) || st.Parts != int64(len(edgePaths)) {
		t.Fatalf("first run: written %d, in parts %d", st.Written, st.Parts)
	}
	// The same pages with a new footer, as a change to layout.html renders
	// them, under the frame of that layout: the schema is the same.
	const footer, newFooter = `<footer class="footer">`, `<footer class="footer footer--new"><p>new</p>`
	moved := &Layout{}
	for _, f := range l.Frames {
		g := f
		g.Text = append([]string(nil), f.Text...)
		g.Text[len(g.Text)-1] = strings.Replace(g.Text[len(g.Text)-1], footer, newFooter, 1)
		moved.Frames = append(moved.Frames, g)
	}
	o.Layout = moved
	newSite := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, r)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		w.Write(bytes.Replace(rec.Body.Bytes(), []byte(footer), []byte(newFooter), 1))
	})
	st, err = Run(context.Background(), newSite, o)
	if err != nil {
		t.Fatal(err)
	}
	if st.Written != 0 || st.Unchanged != int64(len(edgePaths)) {
		t.Fatalf("second run: written %d, unchanged %d", st.Written, st.Unchanged)
	}
	m, _ := ReadManifest(filepath.Join(out, ".export", "manifest.jsonl"))
	for _, p := range edgePaths {
		if !strings.Contains(m[p].Layout, "/") {
			t.Errorf("%s: manifest has no frame: %+v", p, m[p])
		}
	}
}

func TestFrameObjectsAndPointer(t *testing.T) {
	_, l := edgeSite(t)
	var refs []LayoutRef
	for _, f := range l.Frames {
		b, id := FrameObject(f)
		back, err := ParseFrameObject(b)
		if err != nil || back.Schema != f.Schema || strings.Join(back.Text, "") != strings.Join(f.Text, "") {
			t.Fatalf("frame object does not read back: %v", err)
		}
		if _, id2 := FrameObject(f); id2 != id || len(id) != 24 {
			t.Fatalf("frame id %q is not stable", id)
		}
		refs = append(refs, LayoutRef{Key: f.Lang + "/" + f.Schema, ID: id})
	}
	ptr, changed, err := MergeLayoutPointer(nil, refs)
	if err != nil || !changed {
		t.Fatal(err)
	}
	again, changed, _ := MergeLayoutPointer(ptr, refs)
	if changed || !bytes.Equal(again, ptr) {
		t.Fatal("merging the same frames changed the pointer")
	}
	// a new schema goes first; the old one stays for pages not split again
	next, changed, _ := MergeLayoutPointer(ptr, []LayoutRef{{Key: "ar/newschema", ID: "n"}})
	var p LayoutPointer
	_ = json.Unmarshal(next, &p)
	if !changed || p.Layouts[0].Key != "ar/newschema" || len(p.Layouts) != 4 { // the new one, then ar, en and fr
		t.Fatalf("pointer: %s", next)
	}
	for i := 0; i < 10; i++ {
		next, _, _ = MergeLayoutPointer(next, []LayoutRef{{Key: "ar/s" + string(rune('a'+i)), ID: "x"}})
	}
	_ = json.Unmarshal(next, &p)
	ar := 0
	for _, r := range p.Layouts {
		if strings.HasPrefix(r.Key, "ar/") {
			ar++
		}
	}
	if ar != pointerKeep {
		t.Errorf("pointer keeps %d Arabic frames, want %d", ar, pointerKeep)
	}
}

// A commit that changes only the shared frame of layout.html renders no
// page; one that moves a hole, or touches the page parts, renders all.
func TestLayoutFrameOnlyChange(t *testing.T) {
	d := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = d
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
		return strings.TrimSpace(string(b))
	}
	write := func(name, body string) {
		t.Helper()
		os.MkdirAll(filepath.Dir(filepath.Join(d, name)), 0755)
		os.WriteFile(filepath.Join(d, name), []byte(body), 0644)
	}
	git("init", "-q")
	git("config", "user.email", "fixture@example.org")
	git("config", "user.name", "Fixture")
	src := lsite.LayoutSource()
	write(layoutFile, src)
	write("data/tables/legislation/COLUMNS", "id,kind,issued_on,title")
	write("data/tables/legislation/000.tsv", "1\tقانون\t2025-01-01\tA\n")
	git("add", ".")
	git("commit", "-qm", "frame")
	base := git("rev-parse", "HEAD")
	plan := func() ChangePlan {
		t.Helper()
		p, err := PlanChanges(context.Background(), d, base, false)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	write(layoutFile, strings.Replace(src, `<footer class="footer">`, `<footer class="footer"><p class="tiny">{{t "brand.name"}}</p>`, 1))
	git("commit", "-qam", "footer")
	if p := plan(); p.Full || strings.Join(p.Paths, " ") != "/en/search/ /fr/search/ /search/" {
		t.Fatalf("a footer change: %+v, want only the pages stored whole", p)
	}
	write(layoutFile, strings.Replace(src, `<script defer src="/main.js?v={{assetVersion}}"></script>`, `<script defer src="/main2.js?v={{assetVersion}}"></script>`, 1))
	git("commit", "-qam", "scripts")
	if p := plan(); p.Full {
		t.Fatal("a script tag change rendered every page")
	}
	write(layoutFile, strings.Replace(src, `<body>`, `<body><p>{{template "page:desc" .}}</p>`, 1))
	git("commit", "-qam", "new hole")
	if p := plan(); !p.Full {
		t.Fatal("a new hole must render every page")
	}
	write(layoutFile, src+`{{define "x"}}{{end}}`)
	git("commit", "-qam", "not a frame")
	if p := plan(); !p.Full {
		t.Fatal("a layout.html that is not a frame must render every page")
	}
	write(layoutFile, src)
	write("internal/site/templates/_page.html", "x")
	git("add", ".")
	git("commit", "-qm", "parts")
	// The page parts are content: the command maps them through the render
	// record (PlanContent), and a partial every page reaches renders them all.
	if p := plan(); p.Full || strings.Join(p.Content, " ") != "internal/site/templates/_page.html" {
		t.Fatalf("a change to the page parts goes to the content planner: %+v", p)
	}
}
