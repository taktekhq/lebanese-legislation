package mcpserver

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// testdata/golden.json holds the Worker's own answers (its TypeScript
// functions run under Node) for the pure parts of the server: Arabic
// normalization, FTS5 expressions, article numbers, the glossary, the
// rescue readings, the Gazette and date parsers, client kinds, badges and
// analytics ids. Each Go function must give the same answer for every input.

func loadGolden(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g map[string]json.RawMessage
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func rows(t *testing.T, raw json.RawMessage) [][]json.RawMessage {
	t.Helper()
	var r [][]json.RawMessage
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func str(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return s
}

// sameJSON compares a Go value with the Worker's JSON answer.
func sameJSON(t *testing.T, got any, want json.RawMessage) bool {
	t.Helper()
	gb, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	if err := json.Unmarshal(gb, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(g, w)
}

func TestGoldenStrings(t *testing.T) {
	g := loadGolden(t)
	funcs := map[string]func(string) any{
		"arNorm":        func(s string) any { return arNorm(s) },
		"fts5MatchAll":  func(s string) any { return fts5MatchAll(s) },
		"fts5Forms":     func(s string) any { return fts5Forms(s) },
		"lightStem":     func(s string) any { return lightStem(s) },
		"numberForms":   func(s string) any { return numberForms(s) },
		"foldDigits":    func(s string) any { return foldDigits(s) },
		"latinTokens":   func(s string) any { return orEmpty(latinTokens(s)) },
		"latinFold":     func(s string) any { return latinFold(s) },
		"nameVariants":  func(s string) any { return orEmpty(nameVariants(s)) },
		"pinKey":        func(s string) any { return pinKey(s) },
		"pinnedLaw":     func(s string) any { return pinnedLaw(s) },
		"fts5French":    func(s string) any { return fts5French(s) },
		"fts5English":   func(s string) any { return fts5English(s) },
		"gazetteNumber": func(s string) any { return gazetteNumber(s) },
		"heading":       func(s string) any { return heading(s) },
		"snippet":       func(s string) any { return snippet(s) },
		"glossQueryWhole": func(s string) any {
			ar, whole := glossQueryWhole(s)
			return map[string]any{"ar": ar, "whole": whole}
		},
		"splitNumbers": func(s string) any {
			num, year, words := splitNumbers(s)
			return map[string]any{"num": num, "year": year, "words": orEmpty(words)}
		},
		"gazetteQuery": func(s string) any {
			num, year := gazetteQuery(s)
			return map[string]any{"num": num, "year": year}
		},
		"rescueReadings": func(s string) any {
			out := []any{}
			for _, r := range rescueReadings(foldDigits(jsTrim(s))) {
				out = append(out, map[string]any{"query": r.query, "num": r.num, "year": r.year, "kind": r.kind})
			}
			return out
		},
	}
	for name, f := range funcs {
		raw, has := g[name]
		if !has {
			t.Errorf("golden.json has no %s", name)
			continue
		}
		for _, r := range rows(t, raw) {
			in := str(t, r[0])
			if got := f(in); !sameJSON(t, got, r[1]) {
				t.Errorf("%s(%q) = %s, the Worker gives %s", name, in, mustMarshal(got), r[1])
			}
		}
	}
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func TestGoldenDateBound(t *testing.T) {
	for _, r := range rows(t, loadGolden(t)["dateBound"]) {
		var in any
		_ = decodeJSON(r[0], &in)
		var upper bool
		_ = json.Unmarshal(r[1], &upper)
		var want *string
		_ = json.Unmarshal(r[2], &want)
		got := dateBound(in, upper)
		if (want == nil && got != "") || (want != nil && got != *want) {
			t.Errorf("dateBound(%s, %v) = %q, the Worker gives %s", r[0], upper, got, r[2])
		}
	}
}

func TestGoldenClientKind(t *testing.T) {
	for _, r := range rows(t, loadGolden(t)["clientKind"]) {
		ua, name, want := str(t, r[0]), str(t, r[1]), str(t, r[2])
		if got := clientKind(ua, name); got != want {
			t.Errorf("clientKind(%q, %q) = %q, the Worker gives %q", ua, name, got, want)
		}
	}
}

func TestGoldenIDs(t *testing.T) {
	g := loadGolden(t)
	for _, r := range rows(t, g["mcpSessionId"]) {
		if got := mcpSessionID(str(t, r[0]), str(t, r[1])); got != str(t, r[2]) {
			t.Errorf("mcpSessionID(%s, %s) = %s, the Worker gives %s", r[0], r[1], got, r[2])
		}
	}
	for _, r := range rows(t, g["visitorIds"]) {
		c, s := visitorIDs(str(t, r[0]), str(t, r[1]), str(t, r[2]))
		if !sameJSON(t, map[string]string{"clientId": c, "sessionId": s}, r[3]) {
			t.Errorf("visitorIDs(%s, %s, %s) = %s %s, the Worker gives %s", r[0], r[1], r[2], c, s, r[3])
		}
	}
	for _, r := range rows(t, g["visitorId"]) {
		if got := visitorID(str(t, r[0]), str(t, r[1]), str(t, r[2])); got != str(t, r[3]) {
			t.Errorf("visitorID(%s, %s, %s) = %s, the Worker gives %s", r[0], r[1], r[2], got, r[3])
		}
	}
}

func TestGoldenBadges(t *testing.T) {
	g := loadGolden(t)
	for _, r := range rows(t, g["thousands"]) {
		var n int
		_ = json.Unmarshal(r[0], &n)
		if got := thousands(n); got != str(t, r[1]) {
			t.Errorf("thousands(%d) = %s, the Worker gives %s", n, got, r[1])
		}
	}
	for _, r := range rows(t, g["textWidth"]) {
		var want int
		_ = json.Unmarshal(r[1], &want)
		if got := textWidth(str(t, r[0])); got != want {
			t.Errorf("textWidth(%s) = %d, the Worker gives %d", r[0], got, want)
		}
	}
	for _, r := range rows(t, g["renderBadge"]) {
		if got := renderBadge(str(t, r[0]), str(t, r[1]), str(t, r[2])); got != str(t, r[3]) {
			t.Errorf("renderBadge(%s, %s) =\n%s\nthe Worker gives\n%s", r[0], r[1], got, str(t, r[3]))
		}
	}
	for _, r := range rows(t, g["day"]) {
		if got := badgeDay(str(t, r[0])); got != str(t, r[1]) {
			t.Errorf("badgeDay(%s) = %s, the Worker gives %s", r[0], got, r[1])
		}
	}
}

func TestGoldenGAPayload(t *testing.T) {
	var want []json.RawMessage
	if err := json.Unmarshal(loadGolden(t)["gaPayload"], &want); err != nil {
		t.Fatal(err)
	}
	got := []obj{
		gaPayload("1.2", "99", location{"LB", "Beirut", "BA"}, []gaEvent{{name: "mcp_tool_call", params: obj{{"tool_name", "get_law"}, {"search_term", nil}, {"search_kind", ""}}}}),
		gaPayload("1.2", "9", location{}, []gaEvent{{name: "e", params: obj{{"search_term", stringsRepeat("ق", 300)}, {"n", 3}}}}),
	}
	for i := range got {
		if b := mustMarshal(got[i]); !sameJSON(t, got[i], want[i]) {
			t.Errorf("gaPayload %d = %s, the Worker gives %s", i, b, want[i])
		}
	}
}

func stringsRepeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
