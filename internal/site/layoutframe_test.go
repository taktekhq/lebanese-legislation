package site

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The frame of each language, with its pieces between the page's parts,
// must give back the page the server renders: same bytes.
func TestLayoutFramesRebuildPages(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.SetAnalytics(true)
	frames, err := s.LayoutFrames()
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || frames[0].Lang != "ar" || frames[1].Lang != "en" {
		t.Fatalf("frames: %+v", frames)
	}
	for _, f := range frames {
		if f.Slots[len(f.Slots)-1] != "page:content" {
			t.Errorf("%s: the last hole is %s, want page:content", f.Lang, f.Slots[len(f.Slots)-1])
		}
		frame := strings.Join(f.Text, "")
		for _, tag := range []string{`src="/ga.js?v=`, `src="/assets/load.js?v=`, `data-ph="/ph.js?v=`, `<footer class="footer">`, `<header class="nav" id="nav">`} {
			if !strings.Contains(frame, tag) {
				t.Errorf("%s frame has no %s", f.Lang, tag)
			}
		}
	}
	for _, p := range []string{"/blog/", "/en/blog/", "/ai/", "/en/ai/", "/contribute/", "/en/contribute/", "/no-such-page/", "/en/no-such-page/"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		body := w.Body.String()
		f := frames[1]
		if !strings.HasPrefix(p, "/en/") {
			f = frames[0]
		}
		// cut in order, as the exporter does
		rest, ok := strings.CutPrefix(body, f.Text[0])
		if !ok {
			t.Fatalf("%s: does not start with the %s frame", p, f.Lang)
		}
		var parts []string
		for i := 1; i < len(f.Text)-1; i++ {
			j := strings.Index(rest, f.Text[i])
			if j < 0 {
				t.Fatalf("%s: piece %d of the frame not found", p, i)
			}
			parts = append(parts, rest[:j])
			rest = rest[j+len(f.Text[i]):]
		}
		last, ok := strings.CutSuffix(rest, f.Text[len(f.Text)-1])
		if !ok {
			t.Fatalf("%s: does not end with the frame", p)
		}
		parts = append(parts, last)
		var b strings.Builder
		for i, part := range parts {
			b.WriteString(f.Text[i] + part)
		}
		b.WriteString(f.Text[len(f.Text)-1])
		if b.String() != body {
			t.Errorf("%s: frame and parts do not make the page", p)
		}
		if strings.Contains(strings.Join(parts[:len(parts)-1], ""), "<footer") {
			t.Errorf("%s: a head part holds the footer", p)
		}
	}
}

// The frame may not take anything from the Page, or every page would be
// frozen with the value of the first; it may not define templates either.
func TestLayoutFrameRejectsPageFields(t *testing.T) {
	src := LayoutSource()
	for name, bad := range map[string]string{
		"field":  strings.Replace(src, `<body>`, `<body data-x="{{.Title}}">`, 1),
		"if":     strings.Replace(src, `<body>`, `<body>{{if .NoIndex}}x{{end}}`, 1),
		"define": src + `{{define "x"}}y{{end}}`,
		"call":   strings.Replace(src, `<body>`, `<body>{{template "search" ""}}`, 1),
	} {
		if _, err := LayoutSchemas(bad); err == nil {
			t.Errorf("%s: a layout that uses the page was accepted", name)
		}
	}
}

// A change to the frame's own markup keeps the schema; moving a hole to a
// place where its text is escaped differently, or adding one, changes it.
func TestLayoutSchemas(t *testing.T) {
	src := LayoutSource()
	base, err := LayoutSchemas(src)
	if err != nil {
		t.Fatal(err)
	}
	same, err := LayoutSchemas(strings.Replace(src, `<footer class="footer">`, `<footer class="footer footer--new"><p>{{t "brand.name"}}</p>`, 1))
	if err != nil {
		t.Fatal(err)
	}
	if same["ar"] != base["ar"] || same["en"] != base["en"] {
		t.Errorf("a footer change changed the schema: %v, %v", base, same)
	}
	for name, changed := range map[string]string{
		"moved":   strings.Replace(src, `<title>{{template "page:title" .}}</title>`, `<title></title><script>var t = {{template "page:title" .}};</script>`, 1),
		"another": strings.Replace(src, `<body>`, `<body><p>{{template "page:desc" .}}</p>`, 1),
	} {
		got, err := LayoutSchemas(changed)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got["ar"] == base["ar"] {
			t.Errorf("%s: the schema did not change", name)
		}
	}
}
