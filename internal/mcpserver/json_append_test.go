package mcpserver

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

// refObj is obj with the encoder it had before appendJSON: every value
// marshalled on its own, nested objects re-read by the level above.
type refObj []kv

func (o refObj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, e := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := refMarshal(e.K)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		vb, err := refMarshal(e.V)
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func refMarshal(v any) ([]byte, error) {
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

// toRef is v with every obj turned into a refObj.
func toRef(v any) any {
	switch x := v.(type) {
	case obj:
		if x == nil {
			return refObj(nil)
		}
		out := make(refObj, len(x))
		for i, e := range x {
			out[i] = kv{e.K, toRef(e.V)}
		}
		return out
	case []any:
		if x == nil {
			return x
		}
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = toRef(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = toRef(e)
		}
		return out
	}
	return v
}

var jsonPieces = []string{"", "a", "Z9", " ", "قانون", "المادة ٥٠", "\"", "\\", "\\u2028", "\\\"", "<", ">", "&", "\u2028", "\u2029",
	"\x7f", "\u00a0", "\ufeff", "\U0001F600", "\xff", "\xc3", "\xed\xa0\x80", "/", "'", "\t", "\n", "\r", "\b", "\f", "\x00", "\x1f", "\x01", "é", "%20", "{}", "[1]"}

func randString(r *rand.Rand) string {
	var sb strings.Builder
	for n := r.Intn(6); n > 0; n-- {
		sb.WriteString(jsonPieces[r.Intn(len(jsonPieces))])
	}
	return sb.String()
}

func randValue(r *rand.Rand, depth int) any {
	k := r.Intn(16)
	if depth > 4 && k >= 11 {
		k = r.Intn(11)
	}
	s := randString(r)
	switch k {
	case 0, 1, 2:
		return s
	case 3:
		return r.Intn(2000) - 1000
	case 4:
		return int64(r.Int63())
	case 5:
		return r.Intn(2) == 0
	case 6:
		return nil
	case 7:
		return []float64{1.5, 1e21, 1e-7, 0.1, 100}[r.Intn(5)]
	case 8:
		if r.Intn(2) == 0 {
			return (*string)(nil)
		}
		return &s
	case 9:
		if r.Intn(3) == 0 {
			return []string(nil)
		}
		return []string{s, randString(r)}
	case 10:
		return json.RawMessage(`  {"a" : [1, " x"] }`)
	case 11:
		if r.Intn(5) == 0 {
			return obj(nil)
		}
		o := obj{}
		for n := r.Intn(5); n > 0; n-- {
			o = append(o, kv{randString(r), randValue(r, depth+1)})
		}
		return o
	case 12, 13:
		if r.Intn(5) == 0 {
			return []any(nil)
		}
		var a []any = []any{}
		for n := r.Intn(4); n > 0; n-- {
			a = append(a, randValue(r, depth+1))
		}
		return a
	case 14:
		m := map[string]any{}
		for n := r.Intn(3); n > 0; n-- {
			m[randString(r)] = randValue(r, depth+1)
		}
		return m
	}
	return args{"q": s, "n": float64(r.Intn(9))}
}

// TestAppendJSONMatchesEncoder: marshal writes the bytes the encoder wrote
// before appendJSON, for every kind of value a reply holds and strings with
// every character that needs care.
func TestAppendJSONMatchesEncoder(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		v := randValue(r, 0)
		got, gerr := marshal(v)
		want, werr := refMarshal(toRef(v))
		if (gerr != nil) != (werr != nil) || !bytes.Equal(got, want) {
			t.Fatalf("value %#v\n got %q (%v)\nwant %q (%v)", v, got, gerr, want, werr)
		}
		// a reply's text, as tools/call wraps it
		wrapped := obj{{"content", []any{obj{{"type", "text"}, {"text", string(got)}}}}, {"isError", false}}
		got2, _ := marshal(wrapped)
		want2, _ := refMarshal(toRef(wrapped))
		if !bytes.Equal(got2, want2) {
			t.Fatalf("wrapped %q\n got %q\nwant %q", got, got2, want2)
		}
	}
	for _, s := range jsonPieces {
		got, _ := marshal(obj{{s, s}})
		want, _ := refMarshal(refObj{{s, s}})
		if !bytes.Equal(got, want) {
			t.Errorf("%q: got %q, want %q", s, got, want)
		}
	}
	// a value the encoder refuses fails the same way
	if _, err := marshal(obj{{"x", []any{obj{{"nan", func() {}}}}}}); err == nil {
		t.Error("a func value encoded without an error")
	}
}
