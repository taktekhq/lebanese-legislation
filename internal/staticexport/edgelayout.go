package staticexport

// Edge layout: a page is stored in R2 as its own parts only, and the Worker
// puts them into the shared frame of its language when it answers
// (workers/static-site/src/layout.js). The header, footer, menu and
// analytics tags live in one small frame object, so changing them uploads
// that object and no page.
//
// A page in parts is one line of ASCII and the parts' bytes:
//
//	LLP1 <lang> <schema> <len>,<len>,...\n<part><part>...
//
// lens are byte counts, one per hole of the frame. The frame of a language
// and schema is _layout/<id>.json, {"lang","schema","slots","text"}, where id
// is the start of the object's sha256; _export/layout.json names the frame
// of each lang/schema the pages may carry. Frames are never overwritten, so
// a Worker can keep one for as long as it likes.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	lsite "github.com/taktekhq/lebanese-legislation/internal/site"
)

// PartsMagic starts every page stored in parts. No HTML page starts so.
const PartsMagic = "LLP1 "

// LayoutPointerKey is the bucket key of the list of current frames.
const LayoutPointerKey = "_export/layout.json"

// Layout is the frames an export splits its pages with.
type Layout struct {
	Frames []lsite.LayoutFrame
}

// NewLayout checks the frames: each piece of frame between two holes must
// be there to find, or a page could not be cut.
func NewLayout(frames []lsite.LayoutFrame) (*Layout, error) {
	for _, f := range frames {
		if len(f.Text) != len(f.Slots)+1 || f.Lang == "" || f.Schema == "" {
			return nil, fmt.Errorf("layout %s: malformed frame", f.Lang)
		}
		for i := 1; i < len(f.Text)-1; i++ {
			if f.Text[i] == "" {
				return nil, fmt.Errorf("layout %s: empty frame between %s and %s", f.Lang, f.Slots[i-1], f.Slots[i])
			}
			// An escaped value holds no < or ", so a piece that starts with
			// one cannot be found inside the part before it (see cut).
			if c := f.Text[i][0]; c != '<' && c != '"' {
				return nil, fmt.Errorf("layout %s: the frame after %s starts with %q; start it with a tag or close the attribute", f.Lang, f.Slots[i-1], c)
			}
		}
	}
	return &Layout{Frames: frames}, nil
}

// Split cuts an HTML page into its parts under the frame it was rendered
// in; false when it fits none (a page outside the layout, or one a Transform
// changed outside its parts), and then the page is stored whole. Joining the
// parts back into the frame gives body exactly, checked here, so a stored
// page in parts is always the page that was rendered.
func (l *Layout) Split(body []byte) ([]byte, string, bool) {
	for i := range l.Frames {
		f := &l.Frames[i]
		parts, ok := cut(f, body)
		if !ok {
			continue
		}
		var b bytes.Buffer
		b.WriteString(PartsMagic + f.Lang + " " + f.Schema + " ")
		for j, p := range parts {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Itoa(len(p)))
		}
		b.WriteByte('\n')
		for _, p := range parts {
			b.Write(p)
		}
		obj := b.Bytes()
		if back, err := joinParts(f, obj); err != nil || !bytes.Equal(back, body) {
			continue
		}
		return obj, f.Lang + "/" + f.Schema, true
	}
	return nil, "", false
}

// cut finds the pieces of the frame in body, in order: each part is what
// lies between two of them. The parts before the last are escaped values
// and attributes that cannot hold the next piece (each starts with < or "),
// so the first match is the piece; the last part, the page's content, runs
// to the frame's closing piece at the very end.
func cut(f *lsite.LayoutFrame, body []byte) ([][]byte, bool) {
	n := len(f.Slots)
	if !bytes.HasPrefix(body, []byte(f.Text[0])) || !bytes.HasSuffix(body, []byte(f.Text[n])) {
		return nil, false
	}
	end := len(body) - len(f.Text[n])
	cur := len(f.Text[0])
	parts := make([][]byte, 0, n)
	for i := 1; i < n; i++ {
		j := bytes.Index(body[cur:end], []byte(f.Text[i]))
		if j < 0 {
			return nil, false
		}
		parts = append(parts, body[cur:cur+j])
		cur += j + len(f.Text[i])
	}
	if cur > end {
		return nil, false
	}
	return append(parts, body[cur:end]), true
}

// PartsHeader reads the first line of a page in parts.
func PartsHeader(obj []byte) (lang, schema string, lens []int, rest []byte, err error) {
	if !bytes.HasPrefix(obj, []byte(PartsMagic)) {
		return "", "", nil, nil, errors.New("not a page in parts")
	}
	nl := bytes.IndexByte(obj, '\n')
	if nl < 0 {
		return "", "", nil, nil, errors.New("page in parts: no header line")
	}
	f := strings.Split(string(obj[len(PartsMagic):nl]), " ")
	if len(f) != 3 {
		return "", "", nil, nil, errors.New("page in parts: bad header")
	}
	for _, s := range strings.Split(f[2], ",") {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return "", "", nil, nil, errors.New("page in parts: bad length")
		}
		lens = append(lens, n)
	}
	return f[0], f[1], lens, obj[nl+1:], nil
}

// joinParts puts a page in parts back into its frame.
func joinParts(f *lsite.LayoutFrame, obj []byte) ([]byte, error) {
	lang, schema, lens, rest, err := PartsHeader(obj)
	if err != nil {
		return nil, err
	}
	if lang != f.Lang || schema != f.Schema || len(lens) != len(f.Slots) {
		return nil, fmt.Errorf("page in parts %s/%s does not fit frame %s/%s", lang, schema, f.Lang, f.Schema)
	}
	var b bytes.Buffer
	for i, n := range lens {
		if n > len(rest) {
			return nil, errors.New("page in parts: truncated")
		}
		b.WriteString(f.Text[i])
		b.Write(rest[:n])
		rest = rest[n:]
	}
	if len(rest) != 0 {
		return nil, errors.New("page in parts: bytes after the last part")
	}
	b.WriteString(f.Text[len(lens)])
	return b.Bytes(), nil
}

// Join is the whole page of a stored object: a page in parts put into its
// frame, anything else as it is.
func (l *Layout) Join(obj []byte) ([]byte, error) {
	if !bytes.HasPrefix(obj, []byte(PartsMagic)) {
		return obj, nil
	}
	lang, schema, _, _, err := PartsHeader(obj)
	if err != nil {
		return nil, err
	}
	for i := range l.Frames {
		if l.Frames[i].Lang == lang && l.Frames[i].Schema == schema {
			return joinParts(&l.Frames[i], obj)
		}
	}
	return nil, fmt.Errorf("no frame %s/%s for a page in parts", lang, schema)
}

// frameObject is the JSON of a frame in the bucket.
type frameObject struct {
	Lang   string   `json:"lang"`
	Schema string   `json:"schema"`
	Slots  []string `json:"slots"`
	Text   []string `json:"text"`
}

// FrameObject is the stored form of a frame and its id, the key being
// _layout/<id>.json. The id changes whenever a byte of the frame does.
func FrameObject(f lsite.LayoutFrame) (body []byte, id string) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(frameObject{f.Lang, f.Schema, f.Slots, f.Text})
	sum := sha256.Sum256(b.Bytes())
	return b.Bytes(), hex.EncodeToString(sum[:])[:24]
}

// FrameKey is the bucket key of a frame object.
func FrameKey(id string) string { return "_layout/" + id + ".json" }

// ParseFrameObject reads a stored frame back.
func ParseFrameObject(b []byte) (lsite.LayoutFrame, error) {
	var o frameObject
	if err := json.Unmarshal(b, &o); err != nil {
		return lsite.LayoutFrame{}, err
	}
	if len(o.Text) != len(o.Slots)+1 {
		return lsite.LayoutFrame{}, errors.New("frame object: text and slots do not match")
	}
	return lsite.LayoutFrame{Lang: o.Lang, Schema: o.Schema, Slots: o.Slots, Text: o.Text}, nil
}

// LayoutPointer is _export/layout.json: the frame id of each lang/schema,
// newest first. Frames of older schemas stay listed, so pages not yet split
// again still find the frame they were cut from.
type LayoutPointer struct {
	Layouts []LayoutRef `json:"layouts"`
}

// LayoutRef is one entry of the pointer.
type LayoutRef struct {
	Key string `json:"key"` // lang/schema, as in a page's header
	ID  string `json:"id"`  // the frame object, _layout/<id>.json
}

// pointerKeep is how many schemas per language the pointer remembers.
const pointerKeep = 4

// MergeLayoutPointer puts frames (by lang/schema and id) at the head of the
// pointer prev (nil or empty when there is none) and says whether it changed.
func MergeLayoutPointer(prev []byte, refs []LayoutRef) ([]byte, bool, error) {
	var p LayoutPointer
	if len(bytes.TrimSpace(prev)) > 0 {
		if err := json.Unmarshal(prev, &p); err != nil {
			return nil, false, fmt.Errorf("%s: %w", LayoutPointerKey, err)
		}
	}
	next := LayoutPointer{Layouts: append([]LayoutRef(nil), refs...)}
	perLang := map[string]int{}
	for _, r := range refs {
		perLang[langOf(r.Key)]++
	}
	for _, r := range p.Layouts {
		dup := false
		for _, n := range refs {
			if n.Key == r.Key {
				dup = true
			}
		}
		if dup || perLang[langOf(r.Key)] >= pointerKeep {
			continue
		}
		perLang[langOf(r.Key)]++
		next.Layouts = append(next.Layouts, r)
	}
	b, err := json.Marshal(next)
	if err != nil {
		return nil, false, err
	}
	b = append(b, '\n')
	return b, !bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(prev)), nil
}

func langOf(key string) string { l, _, _ := strings.Cut(key, "/"); return l }

// ReadLayoutDir reads the frames an export wrote under dir (its .export:
// _layout/<id>.json), for reading pages in parts back whole. A directory
// with none gives an empty Layout, which leaves every object as it is.
func ReadLayoutDir(dir string) (*Layout, error) {
	files, err := filepath.Glob(filepath.Join(dir, "_layout", "*.json"))
	if err != nil {
		return nil, err
	}
	l := &Layout{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		fr, err := ParseFrameObject(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		l.Frames = append(l.Frames, fr)
	}
	return l, nil
}
