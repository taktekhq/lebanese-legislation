package amend

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type numberCase struct {
	Raw    string   `json:"raw"`
	Target []string `json:"target"`
	Want   []string `json:"want"`
	Parts  []string `json:"parts"`
}

func loadNumberCases(t *testing.T) []numberCase {
	t.Helper()
	b, err := os.ReadFile("../../testdata/article_numbers.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Cases []numberCase }
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) < 40 {
		t.Fatalf("only %d cases", len(f.Cases))
	}
	return f.Cases
}

func TestCanon(t *testing.T) {
	for _, c := range loadNumberCases(t) {
		got := Canon(c.Raw, c.Target)
		if len(got) == 0 && len(c.Want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.Want) {
			t.Errorf("Canon(%q, %v) = %q, want %q", c.Raw, c.Target, got, c.Want)
		}
	}
}

func TestResolveParts(t *testing.T) {
	for _, c := range loadNumberCases(t) {
		if c.Parts == nil {
			continue
		}
		set := map[string]bool{}
		for _, n := range c.Target {
			set[n] = true
		}
		var arts, parts []string
		for _, n := range Resolve(c.Raw, func(s string) bool { return set[s] }) {
			arts, parts = append(arts, n.Article), append(parts, n.Part)
			if !n.Found {
				t.Errorf("Resolve(%q): %q not found in %v", c.Raw, n.Article, c.Target)
			}
		}
		if !reflect.DeepEqual(arts, c.Want) || !reflect.DeepEqual(parts, c.Parts) {
			t.Errorf("Resolve(%q, %v) = %q parts %q, want %q parts %q", c.Raw, c.Target, arts, parts, c.Want, c.Parts)
		}
	}
}

// Without a target a spelling is only folded: nothing is looked up, so a
// sub-article stays itself.
func TestCanonNoTarget(t *testing.T) {
	for raw, want := range map[string][]string{
		"19.1":          {"19-1"},
		"٥٨٦ (1)":       {"586-1"},
		"الوحيدة":       {"وحيدة"},
		"103 جديدة":     {"103"},
		"3 و 8":         {"3", "8"},
		"12 bis":        {"12 مكرر"},
		"4 E":           nil,
		"21 الى 24":     {"21", "22", "23", "24"},
		"المادة 5 مكرر": {"5 مكرر"},
	} {
		if got := Canon(raw, nil); !(len(got) == 0 && len(want) == 0) && !reflect.DeepEqual(got, want) {
			t.Errorf("Canon(%q, nil) = %q, want %q", raw, got, want)
		}
	}
}

func TestIsCanonical(t *testing.T) {
	for s, want := range map[string]bool{
		"12": true, "12-1": true, "12 مكرر": true, "14 مكرر 2": true, "وحيدة": true, "1 اصدار": true,
		"12/1": false, "12 1": false, "19.1": false, "3,8": false, "الوحيدة": false, "12 bis": false, "012": false,
		"6-14-1": false, "12 جديدة": false, "": false, "١٢": false,
	} {
		if got := IsCanonical(s); got != want {
			t.Errorf("IsCanonical(%q) = %v", s, got)
		}
	}
}
