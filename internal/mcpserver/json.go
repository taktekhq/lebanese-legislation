package mcpserver

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// obj is a JSON object that keeps its keys in the order they were set, the
// way a JavaScript object literal does, so a tool's answer serializes with
// the same field order as the Worker's JSON.stringify. A value that the
// Worker left undefined is simply never set.
type obj []kv

type kv struct {
	K string
	V any
}

// set adds k, or replaces its value in place (Object.assign semantics: an
// existing key keeps its position).
func (o *obj) set(k string, v any) {
	for i := range *o {
		if (*o)[i].K == k {
			(*o)[i].V = v
			return
		}
	}
	*o = append(*o, kv{k, v})
}

// get returns the value of k, or nil.
func (o obj) get(k string) (any, bool) {
	for _, e := range o {
		if e.K == k {
			return e.V, true
		}
	}
	return nil, false
}

// del removes k.
func (o *obj) del(k string) {
	for i := range *o {
		if (*o)[i].K == k {
			*o = append((*o)[:i], (*o)[i+1:]...)
			return
		}
	}
}

// merge sets every key of p, in p's order (a spread: {...a, ...p}).
func (o *obj) merge(p obj) {
	for _, e := range p {
		o.set(e.K, e.V)
	}
}

// str sets k to s unless s is nil (an SQL NULL: undefined in the Worker).
func (o *obj) str(k string, s *string) {
	if s != nil {
		o.set(k, *s)
	}
}

// nonEmpty sets k to s unless s is nil or "" (a falsy value the Worker skips).
func (o *obj) nonEmpty(k string, s *string) {
	if s != nil && *s != "" {
		o.set(k, *s)
	}
}

func (o obj) MarshalJSON() ([]byte, error) { return appendJSON(nil, o) }

// marshal is json.Marshal without HTML escaping, as JSON.stringify writes
// "<", ">" and "&" as they are. U+2028 and U+2029, which Go always escapes,
// are written raw too, so the bytes match the Worker's.
func marshal(v any) ([]byte, error) { return appendJSON(nil, v) }

// appendJSON appends what encodeJSON writes for v, in one pass: an obj, a
// []any or a []string is written element by element, and a plain string,
// int or bool directly; any other value, and a string that needs more
// escaping than \" and \\, goes through encodeJSON. Before 9 Nov 2026 each
// nested obj was encoded on its own and then re-read by the encoder of the
// level above, so a reply was scanned once per level of nesting: 60% of the
// CPU of get_law, whose replies reach 280 KB (TestAppendJSONMatchesEncoder
// holds the bytes to the encoder's).
func appendJSON(b []byte, v any) ([]byte, error) {
	var err error
	switch x := v.(type) {
	case nil:
		return append(b, "null"...), nil
	case string:
		return appendString(b, x)
	case bool:
		return strconv.AppendBool(b, x), nil
	case int:
		return strconv.AppendInt(b, int64(x), 10), nil
	case int64:
		return strconv.AppendInt(b, x, 10), nil
	case obj:
		b = append(b, '{')
		for i, e := range x {
			if i > 0 {
				b = append(b, ',')
			}
			if b, err = appendString(b, e.K); err != nil {
				return nil, err
			}
			b = append(b, ':')
			if b, err = appendJSON(b, e.V); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	case []any:
		if x == nil {
			return append(b, "null"...), nil
		}
		b = append(b, '[')
		for i, e := range x {
			if i > 0 {
				b = append(b, ',')
			}
			if b, err = appendJSON(b, e); err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	case []string:
		if x == nil {
			return append(b, "null"...), nil
		}
		b = append(b, '[')
		for i, e := range x {
			if i > 0 {
				b = append(b, ',')
			}
			if b, err = appendString(b, e); err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	}
	return appendEncoded(b, v)
}

// appendString writes s as a JSON string. Valid UTF-8 without control
// characters is copied with only " and \ escaped, which is what the encoder
// does with such a string (U+2028 and U+2029 raw, as marshal writes them);
// anything else goes through encodeJSON.
func appendString(b []byte, s string) ([]byte, error) {
	quotes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 {
			return appendEncoded(b, s)
		}
		if c == '"' || c == '\\' {
			quotes++
		}
	}
	if !utf8.ValidString(s) {
		return appendEncoded(b, s)
	}
	b = append(b, '"')
	if quotes == 0 {
		b = append(b, s...)
	} else {
		start := 0
		for i := 0; i < len(s); i++ {
			if c := s[i]; c == '"' || c == '\\' {
				b = append(b, s[start:i]...)
				b = append(b, '\\', c)
				start = i + 1
			}
		}
		b = append(b, s[start:]...)
	}
	return append(b, '"'), nil
}

func appendEncoded(b []byte, v any) ([]byte, error) {
	out, err := encodeJSON(v)
	if err != nil {
		return nil, err
	}
	return append(b, out...), nil
}

// encodeJSON is the encoder appendJSON matches: json.Encoder without HTML
// escaping, U+2028 and U+2029 written raw.
func encodeJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	if bytes.Contains(out, []byte(`\u202`)) {
		out = unescapeLineSeparators(out)
	}
	return out, nil
}

// unescapeLineSeparators turns the   and   escapes Go writes back
// into the characters, leaving an escaped backslash followed by "u2028" alone.
func unescapeLineSeparators(b []byte) []byte {
	var out bytes.Buffer
	for i := 0; i < len(b); i++ {
		if b[i] == '\\' && i+1 < len(b) {
			if b[i+1] == 'u' && i+5 < len(b) && (string(b[i+2:i+6]) == "2028" || string(b[i+2:i+6]) == "2029") {
				if b[i+5] == '8' {
					out.WriteString(" ")
				} else {
					out.WriteString(" ")
				}
				i += 5
				continue
			}
			out.WriteByte(b[i])
			out.WriteByte(b[i+1])
			i++
			continue
		}
		out.WriteByte(b[i])
	}
	return out.Bytes()
}

func mustMarshal(v any) string {
	b, err := marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// ---------------------------------------------------------------- JavaScript string rules

// isJSSpace is JavaScript's whitespace (String.prototype.trim and \s):
// unlike unicode.IsSpace it includes U+FEFF and leaves out U+0085.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsSpace is JavaScript's \s as a character class body for Go's regexp.
const jsSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

func jsTrim(s string) string      { return strings.TrimFunc(s, isJSSpace) }
func jsTrimEnd(s string) string   { return strings.TrimRightFunc(s, isJSSpace) }
func jsTrimStart(s string) string { return strings.TrimLeftFunc(s, isJSSpace) }

// jsFields is s.split(/\s+/).filter(Boolean).
func jsFields(s string) []string { return strings.FieldsFunc(s, isJSSpace) }

// jsLen is a string's .length: UTF-16 code units.
func jsLen(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// jsSlice is s.slice(0, n) in UTF-16 code units; a surrogate pair cut in
// half is dropped rather than written as invalid UTF-8.
func jsSlice(s string, n int) string {
	if n <= 0 {
		return ""
	}
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			return s[:i]
		}
		units += w
	}
	return s
}

// runeLen is [...s].length: code points.
func runeLen(s string) int { return utf8.RuneCountInString(s) }

// jsNumberString formats a number the way String(n) does in JavaScript.
func jsNumberString(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	abs := math.Abs(f)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	// Go writes 1e-07; JavaScript writes 1e-7.
	s = strings.Replace(s, "e-0", "e-", 1)
	s = strings.Replace(s, "e+0", "e+", 1)
	return s
}

// utf16Units is s as UTF-16 code units, for the few places that index a
// string the way JavaScript does.
func utf16Units(s string) []uint16 { return utf16.Encode([]rune(s)) }
