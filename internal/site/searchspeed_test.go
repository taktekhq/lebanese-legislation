package site

import (
	"fmt"
	"strings"
	"testing"
)

func TestTitleConds(t *testing.T) {
	join := func(words ...string) string {
		conds, args := titleConds(words, 1)
		return strings.Join(conds, " AND ") + " " + fmt.Sprint(args...)
	}
	for _, c := range []struct {
		in   []string
		want string
	}{
		{[]string{"2"}, "l.number_int = $1"},
		{[]string{"409"}, "(l.number_int = $1 OR ar_norm(l.title) LIKE ar_norm($2))"},
		{[]string{"قوانين", "الجنسية"}, "l.kind = ANY($1) AND (ar_norm(l.title) LIKE ar_norm($2)"},
		{[]string{"قانون", "تنظيم", "في"}, "(ar_norm(l.title) LIKE ar_norm($1) OR l.kind = ANY($2)) AND ar_norm(l.title) LIKE ar_norm($3)"},
		{[]string{"مرسوم", "اشتراعي", "88"}, "l.kind = ANY($1) AND l.number_int = $2"},
		{[]string{"حماية", "القصّار"}, "ar_norm(l.title) LIKE ar_norm($1) AND (ar_norm(l.title) LIKE ar_norm($2) OR ar_norm(l.title) LIKE ar_norm($3) OR ar_norm(l.title) LIKE ar_norm($4))"},
		{[]string{"العمل", "1946"}, "ar_norm(l.title) LIKE ar_norm($1) AND (ar_norm(l.title) LIKE ar_norm($2) OR (l.issued_on >= make_date($3, 1, 1) AND l.issued_on < make_date($3 + 1, 1, 1)))"},
	} {
		if got := join(c.in...); !strings.HasPrefix(got, c.want+" ") {
			t.Errorf("titleConds(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := join("الجنسيه"); !strings.Contains(got, "التابعية") {
		t.Errorf("الجنسيه should also match التابعية: %q", got)
	}
}

func TestSearchCache(t *testing.T) {
	var c searchCache
	if _, _, ok := c.get("x"); ok {
		t.Fatal("empty cache answered")
	}
	c.put("x", Results{Q: "x", LawCount: 3}, "cite")
	if r, how, ok := c.get("x"); !ok || r.LawCount != 3 || how != "cite" {
		t.Errorf("get = %v %q %v", r, how, ok)
	}
	for i := range searchCacheSize + 1 {
		c.put(fmt.Sprint(i), Results{}, "")
	}
	if len(c.m) > searchCacheSize {
		t.Errorf("cache grew to %d", len(c.m))
	}
}

func TestArticleSource(t *testing.T) {
	if got := articleSource("قانون العمل", "W"); got != "legislation_article a" {
		t.Errorf("words: %q", got)
	}
	for _, q := range []string{"2", "28 1930"} {
		if got := articleSource(q, "W"); !strings.Contains(got, "WHERE W LIMIT 3000") {
			t.Errorf("articleSource(%q) = %q", q, got)
		}
	}
	if onlyDigits("") || onlyDigits(" ") || onlyDigits("2a") {
		t.Error("onlyDigits accepts non-numbers")
	}
}
