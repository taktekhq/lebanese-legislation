package site

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/taktekhq/lebanese-legislation/internal/layoutframe"
	"html/template"
	"io/fs"
	"regexp"
	"strings"
	"text/template/parse"
)

// LayoutFrame is layout.html for one interface language with the page's own
// parts cut out: Text[0], the page's part for Slots[0], Text[1], ... Text[n]
// is the whole page. The static export stores a page as its parts only and
// the edge Worker puts them back into the frame (internal/staticexport,
// workers/static-site), so a change to the header, footer or analytics tags
// is one new frame instead of a new copy of every page.
//
// layout.html may use only what is the same on every page of a language:
// the funcs (t, u, uilang, assetVersion, analytics...) and the "page:"
// templates of _page.html, which hold every use of the Page. The frame is
// rendered with no Page at all, so a field used outside a "page:" template
// fails here instead of being frozen into every page.
type LayoutFrame = layoutframe.Frame

// slotMark stands in for a hole while the frame is rendered. It holds no
// character html/template escapes, so it comes out as written in any context.
const slotMark = "@@llslot:"

var slotRE = regexp.MustCompile(`@@llslot:([a-z:]+)@@`)

// slotProbe is printed into each hole to learn how the hole escapes text:
// the same characters come out differently in text, an attribute or a URL.
const slotProbe = `<>"'& é/?#=:`

// LayoutLangs are the interface languages that have a frame.
var LayoutLangs = []string{"ar", "en", "fr"}

// LayoutFrames are the frames of the layout this server renders pages with,
// one per interface language.
func (s *Server) LayoutFrames() ([]LayoutFrame, error) {
	if s.isStatic {
		return nil, errors.New("layout frames: a static preview has one language and no frames")
	}
	src, err := fs.ReadFile(templateFS, "templates/layout.html")
	if err != nil {
		return nil, err
	}
	return layoutFrames(string(src), s.funcs)
}

// LayoutSchemas are the schemas, by language, of another version of
// layout.html (an earlier commit's): two versions with the same schemas
// differ in the shared frame only, and the pages split under one fit the
// other. It fails when src is not a frame (it defines templates, or calls one
// that is not a "page:" part), and then nothing can be assumed.
func LayoutSchemas(src string) (map[string]string, error) {
	funcs := template.FuncMap{}
	for k, v := range baseFuncs {
		funcs[k] = v
	}
	funcs["static"] = func() bool { return false }
	funcs["analytics"] = func() bool { return true }
	funcs["meaningEnabled"] = func() bool { return false }
	funcs["meaningProvider"] = func() string { return "" }
	funcs["meaningShares"] = func() bool { return false }
	fr, err := layoutFrames(src, funcs)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, f := range fr {
		out[f.Lang] = f.Schema
	}
	return out, nil
}

func layoutFrames(src string, funcs template.FuncMap) ([]LayoutFrame, error) {
	var out []LayoutFrame
	for _, lang := range LayoutLangs {
		f, err := layoutFrame(src, funcs, lang)
		if err != nil {
			return nil, fmt.Errorf("layout frame %s: %w", lang, err)
		}
		out = append(out, f)
	}
	return out, nil
}

func layoutFrame(src string, funcs template.FuncMap, lang string) (LayoutFrame, error) {
	f := LayoutFrame{Lang: lang}
	// The first render has a mark in each hole, the second the probe before
	// the mark: the text between a piece of the frame and the next mark is
	// how that hole escapes.
	marked, err := renderFrame(src, funcs, lang, func(name string) string { return slotMark + name + "@@" })
	if err != nil {
		return f, err
	}
	probed, err := renderFrame(src, funcs, lang, func(name string) string {
		return fmt.Sprintf("{{%q}}%s%s@@", slotProbe, slotMark, name)
	})
	if err != nil {
		return f, err
	}
	locs := slotRE.FindAllStringSubmatchIndex(marked, -1)
	last := 0
	for _, l := range locs {
		f.Text = append(f.Text, marked[last:l[0]])
		f.Slots = append(f.Slots, marked[l[2]:l[3]])
		last = l[1]
	}
	f.Text = append(f.Text, marked[last:])
	for i, t := range f.Text {
		if strings.Contains(t, slotMark) {
			return f, fmt.Errorf("the frame holds the text %q, which marks a hole", slotMark)
		}
		if i > 0 && i < len(f.Text)-1 && t == "" {
			// two holes side by side: a page could not be cut between them
			return f, fmt.Errorf("holes %s and %s touch; put some frame between them or make them one part", f.Slots[i-1], f.Slots[i])
		}
	}
	h := sha256.New()
	rest := probed
	for i, name := range f.Slots {
		if !strings.HasPrefix(rest, f.Text[i]) {
			return f, errors.New("the probed frame does not follow the marked one")
		}
		rest = rest[len(f.Text[i]):]
		mark := slotMark + name + "@@"
		j := strings.Index(rest, mark)
		if j < 0 {
			return f, errors.New("the probed frame does not follow the marked one")
		}
		fmt.Fprintf(h, "%s=%s\n", name, rest[:j])
		rest = rest[j+len(mark):]
	}
	f.Schema = hex.EncodeToString(h.Sum(nil))[:16]
	return f, nil
}

// renderFrame renders layout.html for lang with each "page:" template
// replaced by hole(name), on no data: any use of the Page fails.
func renderFrame(src string, funcs template.FuncMap, lang string, hole func(string) string) (string, error) {
	lf := englishFuncs()
	switch lang {
	case "ar":
		lf = arabicFuncs()
	case "fr":
		lf = frenchFuncs()
	}
	t, err := template.New("layout.html").Funcs(funcs).Funcs(lf).Parse(src)
	if err != nil {
		return "", err
	}
	if len(t.Templates()) != 1 {
		var names []string
		for _, d := range t.Templates() {
			if d.Name() != "layout.html" {
				names = append(names, d.Name())
			}
		}
		return "", fmt.Errorf("layout.html defines %s: shared blocks live in a partial (_blocks.html), page parts in _page.html", strings.Join(names, ", "))
	}
	names, err := frameCalls(t.Tree.Root)
	if err != nil {
		return "", err
	}
	for _, n := range names {
		if _, err := t.New(n).Parse(hole(n)); err != nil {
			return "", err
		}
	}
	var b strings.Builder
	if err := t.Option("missingkey=error").Execute(&b, map[string]any{}); err != nil {
		return "", fmt.Errorf("%w (layout.html may use the Page only through a \"page:\" template)", err)
	}
	return b.String(), nil
}

// frameCalls are the templates the frame calls, each once; every one must
// be a page part.
func frameCalls(n parse.Node) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	var walk func(parse.Node) error
	walk = func(n parse.Node) error {
		switch n := n.(type) {
		case *parse.ListNode:
			if n == nil {
				return nil
			}
			for _, c := range n.Nodes {
				if err := walk(c); err != nil {
					return err
				}
			}
		case *parse.IfNode:
			return walkBranch(walk, &n.BranchNode)
		case *parse.RangeNode:
			return walkBranch(walk, &n.BranchNode)
		case *parse.WithNode:
			return walkBranch(walk, &n.BranchNode)
		case *parse.TemplateNode:
			if !strings.HasPrefix(n.Name, "page:") {
				return fmt.Errorf("layout.html calls %q: the frame may call only \"page:\" templates", n.Name)
			}
			if !seen[n.Name] {
				seen[n.Name] = true
				out = append(out, n.Name)
			}
		}
		return nil
	}
	return out, walk(n)
}

func walkBranch(walk func(parse.Node) error, b *parse.BranchNode) error {
	if err := walk(b.List); err != nil {
		return err
	}
	if b.ElseList != nil {
		return walk(b.ElseList)
	}
	return nil
}

// LayoutSource is the layout.html compiled into this binary.
func LayoutSource() string {
	b, _ := fs.ReadFile(templateFS, "templates/layout.html")
	return string(b)
}
