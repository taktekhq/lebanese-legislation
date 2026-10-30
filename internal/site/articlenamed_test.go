package site

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

func TestArticleNamed(t *testing.T) {
	b, err := os.ReadFile("../../testdata/article_numbers.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct {
			Raw    string
			Target []string
			Want   []string
		}
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Cases {
		want := ""
		for _, w := range c.Want {
			if slices.Contains(c.Target, w) {
				want = w
				break
			}
		}
		if got := articleNamed(c.Raw, c.Target); got != want {
			t.Errorf("articleNamed(%q, %v) = %q, want %q", c.Raw, c.Target, got, want)
		}
	}

	for slug, want := range map[string]string{"12-مكرر": "12 مكرر", "14-مكرر-2": "14 مكرر 2", "12-1": "12-1"} {
		if got := articleNamed(slug, []string{"12", "12 مكرر", "14 مكرر 2", "12-1"}); got != want {
			t.Errorf("articleNamed(%q) = %q, want %q", slug, got, want)
		}
	}
}
