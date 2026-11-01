// Package page reads the Centre's HTML the way a browser would, then pulls
// out text with its paragraph breaks kept.
//
// The site's markup is loose (unclosed headings, class=text-1' with a stray
// quote, a tag literally named "font:"), so everything goes through the
// HTML5 tree builder in golang.org/x/net/html, which repairs it exactly as a
// browser does. Parsers then work on the repaired tree.
package page

import (
	"bytes"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Doc is a parsed page with an index of its elements by id.
type Doc struct {
	Root *html.Node
	ids  map[string]*html.Node
}

// Parse builds the tree and indexes every element that has an id.
func Parse(raw []byte) (*Doc, error) {
	root, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	d := &Doc{Root: root, ids: map[string]*html.Node{}}
	Walk(root, func(n *html.Node) bool {
		if n.Type == html.ElementNode {
			if id := Attr(n, "id"); id != "" {
				if _, seen := d.ids[id]; !seen {
					d.ids[id] = n
				}
			}
		}
		return true
	})
	return d, nil
}

// ID returns the first element with this id, or nil.
func (d *Doc) ID(id string) *html.Node { return d.ids[id] }

// Text is the element's text on one line, or "" when the element is missing.
func (d *Doc) Text(id string) string { return Inline(d.ids[id]) }

// Walk visits n and its descendants in document order; returning false from
// fn skips that node's children.
func Walk(n *html.Node, fn func(*html.Node) bool) {
	if n == nil || !fn(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		Walk(c, fn)
	}
}

// Attr returns an attribute's value, or "".
func Attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// Is reports whether n is an element with this tag name.
func Is(n *html.Node, tag atom.Atom) bool {
	return n != nil && n.Type == html.ElementNode && n.DataAtom == tag
}

// Inline returns all text under n with runs of whitespace (including
// no-break spaces) collapsed to one space.
func Inline(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	Walk(n, func(c *html.Node) bool {
		if c.Type == html.ElementNode && (c.DataAtom == atom.Script || c.DataAtom == atom.Style) {
			return false
		}
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
			b.WriteByte(' ')
		}
		return true
	})
	return Squeeze(b.String())
}

// Squeeze collapses whitespace runs to single spaces and trims the ends.
func Squeeze(s string) string {
	return strings.Join(strings.FieldsFunc(s, isSpace), " ")
}

func isSpace(r rune) bool { return unicode.IsSpace(r) || r == ' ' || r == '‏' || r == '‎' }

// Para is one paragraph of text and whether it sat in a left-to-right block.
type Para struct {
	Text string
	LTR  bool
}

var blocks = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Br: true, atom.Li: true, atom.Tr: true, atom.Td: true,
	atom.Table: true, atom.Ul: true, atom.Ol: true, atom.Blockquote: true, atom.Center: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
}

// Paragraphs splits the text under n at block elements and line breaks.
// Empty paragraphs are dropped. A paragraph is LTR when all its text sits under
// an ancestor inside n that declares direction: ltr (how the Centre marks the
// French text). The Centre also wraps the bare number of an Arabic item in
// dir="ltr" ("<span dir=ltr>4</span> -"), so text partly outside one is not LTR.
// skip, when non-nil, prunes subtrees (they contribute no text).
func Paragraphs(n *html.Node, skip func(*html.Node) bool) []Para {
	var out []Para
	var cur strings.Builder
	curLTR, curRTL := false, false
	flush := func() {
		if t := Squeeze(cur.String()); t != "" {
			out = append(out, Para{Text: t, LTR: curLTR && !curRTL})
		}
		cur.Reset()
		curLTR, curRTL = false, false
	}
	var walk func(*html.Node, bool)
	walk = func(c *html.Node, ltr bool) {
		switch c.Type {
		case html.TextNode:
			if strings.TrimFunc(c.Data, isSpace) != "" {
				cur.WriteString(c.Data)
				curLTR = curLTR || ltr
				curRTL = curRTL || !ltr
			} else {
				cur.WriteByte(' ')
			}
			return
		case html.ElementNode:
			if c.DataAtom == atom.Script || c.DataAtom == atom.Style || (skip != nil && skip(c)) {
				return
			}
			if isLTR(c) {
				ltr = true
			}
			if blocks[c.DataAtom] {
				flush()
			}
		}
		for k := c.FirstChild; k != nil; k = k.NextSibling {
			walk(k, ltr)
		}
		if c.Type == html.ElementNode && blocks[c.DataAtom] {
			flush()
		}
	}
	walk(n, false)
	flush()
	return out
}

func isLTR(n *html.Node) bool {
	if strings.EqualFold(Attr(n, "dir"), "ltr") {
		return true
	}
	st := strings.ReplaceAll(strings.ToLower(Attr(n, "style")), " ", "")
	return strings.Contains(st, "direction:ltr")
}

// Scripts a paragraph can be written in.
const (
	NoLetters = iota
	ArabicScript
	LatinScript
)

// Script says whether a paragraph is mostly Arabic or mostly Latin letters
// (French, English), or has no letters at all.
func Script(s string) int {
	var lat, ar int
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Arabic, r) && unicode.IsLetter(r):
			ar++
		case unicode.Is(unicode.Latin, r):
			lat++
		}
	}
	switch {
	case ar == 0 && lat == 0:
		return NoLetters
	case lat > ar:
		return LatinScript
	}
	return ArabicScript
}

// Join puts paragraphs back together with blank lines between them.
func Join(ps []string) string { return strings.Join(ps, "\n\n") }
