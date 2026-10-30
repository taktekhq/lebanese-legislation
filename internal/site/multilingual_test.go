package site

import (
	"context"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

func TestSearchTopicsContent(t *testing.T) {
	for _, tc := range []struct{ q, slug, lang, excerpt string }{
		{"arduous or unhealthy work", "working-hours-annual-leave", "en", "arduous or unhealthy work"},
		{"RESTAURANTS CAFES", "working-hours-annual-leave", "en", "restaurants and cafés"},
		{"الأشغال المرهقة", "working-hours-annual-leave", "ar", "الأشغال المرهقة"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			hits := searchTopics(tc.q)
			for _, h := range hits {
				if h.Slug == tc.slug && h.Lang == tc.lang {
					if !strings.Contains(h.Snippet, tc.excerpt) {
						t.Fatalf("snippet %q lacks %q", h.Snippet, tc.excerpt)
					}
					if h.Title != topics.Get(tc.slug).Lang(tc.lang).Title {
						t.Fatalf("wrong language title: %q", h.Title)
					}
					return
				}
			}
			t.Fatalf("no matching topic: %#v", hits)
		})
	}
	for _, q := range []string{"", "the and of", "quantum spaceship", "arduous spaceship"} {
		if h := searchTopics(q); len(h) > 0 {
			t.Errorf("query %q returned unrelated hits: %#v", q, h)
		}
	}
}

func TestAddEnglishTitlesPreservesExisting(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	laws, n := s.lawsByEnglishTitle(ctx, "labor", 10)
	if len(laws) == 0 {
		t.Skip("snapshot has no English labor title")
	}
	original := laws[0]
	original.Snippet = "existing Arabic exact hit"
	res := Results{Q: "labor", Laws: []LawRow{original}, LawCount: 1}
	s.addEnglishTitles(ctx, &res)
	if res.Laws[0].ID != original.ID || res.Laws[0].Title != original.Title || res.Laws[0].Snippet != original.Snippet {
		t.Fatal("existing first result changed")
	}
	if res.Laws[0].TitleEn == "" {
		t.Fatal("English title absent from matching result")
	}
	seen := map[int]bool{}
	for _, l := range res.Laws {
		if seen[l.ID] {
			t.Fatalf("duplicate law %d", l.ID)
		}
		seen[l.ID] = true
	}
	if res.LawCount < len(res.Laws) || res.LawCount < n {
		t.Fatalf("count %d smaller than known hits", res.LawCount)
	}
	before := len(res.Laws)
	count := res.LawCount
	s.addEnglishTitles(ctx, &res)
	if len(res.Laws) != before || res.LawCount != count {
		t.Fatal("repeated merge changed results")
	}
}
