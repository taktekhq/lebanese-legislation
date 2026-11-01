package site

import (
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

// The home pages link the topics people ask about most, so search engines and
// assistants that crawl the home page reach the answers in one hop.
func TestHomeTopicSlugsExist(t *testing.T) {
	if len(topics.All()) == 0 {
		t.Skip("no topics yet")
	}
	seen := map[string]bool{}
	for _, slug := range homeTopicSlugs {
		if topics.Get(slug) == nil {
			t.Errorf("home topic %q has no topic file", slug)
		}
		if seen[slug] {
			t.Errorf("home topic %q listed twice", slug)
		}
		seen[slug] = true
	}
	if len(homeTopicSlugs) < 12 {
		t.Errorf("home lists %d topics, want at least 12", len(homeTopicSlugs))
	}
}

func TestHomeTopicsPartial(t *testing.T) {
	if len(topics.All()) == 0 {
		t.Skip("no topics yet")
	}
	s := topicTestServer(t)
	for _, c := range []struct{ lang, prefix string }{{"en", "/en"}, {"ar", ""}} {
		refs := homeTopicRefs(c.lang)
		if len(refs) != len(homeTopicSlugs) {
			t.Fatalf("%s: %d refs, want %d", c.lang, len(refs), len(homeTopicSlugs))
		}
		pages := s.pages
		if c.lang == "ar" {
			pages = s.pagesAr
		}
		var b strings.Builder
		if err := pages["home"].ExecuteTemplate(&b, "hometopics", refs); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		for _, r := range refs {
			if !strings.Contains(out, `href="`+c.prefix+`/topic/`+r.Slug+`/"`) {
				t.Errorf("%s home lacks a link to %s", c.lang, r.Slug)
			}
			if r.Title == "" || !strings.Contains(out, r.Title) {
				t.Errorf("%s home lacks the title of %s", c.lang, r.Slug)
			}
		}
		if !strings.Contains(out, `href="`+c.prefix+`/topics/"`) {
			t.Errorf("%s home lacks the link to all topics", c.lang)
		}
		if strings.Contains(out, "\u2014") {
			t.Errorf("%s home topics contain an em dash", c.lang)
		}
	}
}
