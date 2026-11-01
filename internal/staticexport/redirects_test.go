package staticexport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRanges(t *testing.T) {
	for _, c := range []struct{ in, want []int }{
		{nil, nil},
		{[]int{5}, []int{5, 1}},
		{[]int{3, 1, 2, 7, 8, 20}, []int{1, 3, 7, 2, 20, 1}},
		{[]int{4, 4, 5}, []int{4, 2}},
	} {
		if got := ranges(append([]int(nil), c.in...)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ranges(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

type resolveCases struct {
	Decree  []int      `json:"decree"`
	Text    []int      `json:"text"`
	Aliases [][2]int   `json:"aliases"`
	Pages   []string   `json:"pages"`
	Cases   [][]string `json:"cases"`
}

func loadCases(t *testing.T) resolveCases {
	t.Helper()
	b, err := os.ReadFile("testdata/resolve-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var c resolveCases
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func table() *RedirectData {
	d := &RedirectData{Decree: []int{100, 5}, Text: []int{200, 2}, Aliases: [][2]int{{900001, 101}}}
	d.index()
	return d
}

// The same table drives the Worker's JavaScript (workers/static-site/test).
func TestResolveMatchesTheLiveRules(t *testing.T) {
	c := loadCases(t)
	d := &RedirectData{Decree: c.Decree, Text: c.Text, Aliases: c.Aliases}
	d.index()
	pages := map[string]bool{}
	for _, p := range c.Pages {
		pages[p] = true
	}
	exists := func(p string) bool { return pages[p] }
	for _, cs := range c.Cases {
		if got := d.Resolve(cs[0], exists); got != cs[1] {
			t.Errorf("Resolve(%q) = %q, want %q", cs[0], got, cs[1])
		}
	}
}

func TestCompareNamesTheEmailDecodeScript(t *testing.T) {
	live := []byte(`<body>write <a href="/cdn-cgi/l/email-protection" class="__cf_email__" data-cfemail="ab">[email&#160;protected]</a><script data-cfasync="false" src="/cdn-cgi/scripts/5c5dd728/cloudflare-static/email-decode.min.js"></script></body>`)
	out := []byte(`<body>write bleu@isae.edu.lb</body>`)
	if cats, _ := Compare(live, out); len(cats) != 1 || cats[0] != CatEdge {
		t.Errorf("categories %v, want %q", cats, CatEdge)
	}
}

func TestCompareMasksMailtoLinksAndLeavesPlaceholdersAlone(t *testing.T) {
	live := []byte(`<body><p>Write to <a class="link" href="/cdn-cgi/l/email-protection#ef9b8e">[email&#160;protected]</a>.</p><input placeholder="you@example.com"></body>`)
	out := []byte(`<body><p>Write to <a class="link" href="mailto:team@example.org">team@example.org</a>.</p><input placeholder="you@example.com"></body>`)
	if cats, _ := Compare(live, out); len(cats) != 1 || cats[0] != CatEdge {
		t.Errorf("categories %v, want %q", cats, CatEdge)
	}
}

func TestRedirectDataFromManifestAndRoundTrip(t *testing.T) {
	m := map[string]Entry{
		"/law/7/":           {Path: "/law/7/", Status: 200},
		"/decree/100/":      {Path: "/decree/100/", Status: 200},
		"/decree/101/":      {Path: "/decree/101/", Status: 200},
		"/text/200/":        {Path: "/text/200/", Status: 200},
		"/decree/102/":      {Path: "/decree/102/", Status: 301}, // not a page
		"/decree/100/part/": {Path: "/decree/100/part/", Status: 200},
	}
	d, err := BuildRedirectData(context.Background(), m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.Decree, []int{100, 2}) || !reflect.DeepEqual(d.Text, []int{200, 1}) {
		t.Errorf("Decree=%v Text=%v", d.Decree, d.Text)
	}
	file := filepath.Join(t.TempDir(), "redirects.json")
	if err := WriteRedirectData(file, d); err != nil {
		t.Fatal(err)
	}
	back, err := ReadRedirectData(file)
	if err != nil {
		t.Fatal(err)
	}
	if back.Word(101) != "decree" || back.Word(200) != "text" || back.Word(7) != "law" || back.Word(102) != "law" {
		t.Errorf("words after a round trip: %s %s %s %s", back.Word(101), back.Word(200), back.Word(7), back.Word(102))
	}
}

func TestCompare(t *testing.T) {
	live := []byte("<html><head><title>T</title><link rel=\"canonical\" href=\"/a/\">\n<meta name=\"robots\" content=\"index, follow\">\n<script defer src=\"/ga.js?v=abc\"></script></head>\n<body>\n  <main>hello   world</main></body></html>")
	same := func(b string) []byte { return []byte(b) }
	for _, c := range []struct {
		name string
		out  []byte
		want string
	}{
		{"identical", live, CatIdentical},
		{"whitespace", same(strings.ReplaceAll(string(live), "   ", " ")), CatWhitespace},
		{"build id", same(strings.ReplaceAll(string(live), "?v=abc", "?v=def")), CatAssetOnly},
		{"title", same(strings.ReplaceAll(string(live), "<title>T</title>", "<title>U</title>")), CatTitle},
		{"robots", same(strings.ReplaceAll(string(live), "index, follow", "noindex, follow")), CatRobots},
		{"canonical", same(strings.ReplaceAll(string(live), `href="/a/"`, `href="/b/"`)), CatCanonical},
		{"text", same(strings.ReplaceAll(string(live), "hello", "bye")), CatText},
	} {
		cats, _ := Compare(live, c.out)
		if len(cats) == 0 || cats[0] != c.want {
			t.Errorf("%s: categories %v, want first %q", c.name, cats, c.want)
		}
	}
	// what the edge adds is named, not counted as a difference
	edge := strings.Replace(string(live), "</body>", `<script type="module" src="https://static.cloudflareinsights.com/beacon.min.js/v1" data-cf-beacon='{"token":"x"}'></script></body>`, 1)
	if cats, _ := Compare([]byte(edge), live); len(cats) != 1 || cats[0] != CatEdge {
		t.Errorf("beacon only: %v, want %q", cats, CatEdge)
	}
	mail := strings.Replace(string(live), "hello   world", `write <a href="/cdn-cgi/l/email-protection" class="__cf_email__" data-cfemail="ab">[email&#160;protected]</a>`, 1)
	rawMail := strings.Replace(string(live), "hello   world", "write bleu@isae.edu.lb", 1)
	if cats, _ := Compare([]byte(mail), []byte(rawMail)); len(cats) != 1 || cats[0] != CatEdge {
		t.Errorf("email obfuscation only: %v, want %q", cats, CatEdge)
	}
	if cats, _ := Compare([]byte(edge), []byte(strings.ReplaceAll(string(live), "hello", "bye"))); cats[0] != CatText {
		t.Errorf("beacon plus a real text change: %v, want the text change named", cats)
	}
	if _, ex := Compare(live, []byte(strings.ReplaceAll(string(live), "hello", "bye"))); !strings.Contains(ex, "bye") || !strings.Contains(ex, "hello") {
		t.Errorf("excerpt does not show both sides: %q", ex)
	}
}

func TestOldVariantsAreAddressesOfExistingPages(t *testing.T) {
	m := map[string]Entry{}
	for _, p := range []string{"/law/7/", "/law/8/", "/decree/101/", "/decree/101/article/3/", "/text/200/", "/law/9/article/12/", "/ruling/5/"} {
		m[p] = Entry{Path: p, Status: 200, Type: "text/html"}
	}
	d := table()
	got := OldVariants(m, d, 12, 1)
	if len(got) == 0 {
		t.Fatal("no variants")
	}
	exists := func(p string) bool { _, ok := m[p]; return ok }
	moved := 0
	for _, p := range got {
		if _, ok := m[p]; ok {
			t.Errorf("%q is a current address, not an old one", p)
		}
		if to := d.Resolve(p, exists); to != p {
			moved++
		}
	}
	if moved < len(got)/2 {
		t.Errorf("only %d of %d variants are moved by the table", moved, len(got))
	}
	if again := OldVariants(m, d, 12, 1); !reflect.DeepEqual(again, got) {
		t.Error("variants differ between runs with one seed")
	}
	if indic("12") != "١٢" {
		t.Errorf("indic(12) = %q", indic("12"))
	}
}
