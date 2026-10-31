package site

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/web/internal/site/topics"
)

func topicTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func topicGet(s *Server, path string) (int, string) {
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec.Code, rec.Body.String()
}

func TestTopicsIndexAndPages(t *testing.T) {
	all := topics.All()
	if len(all) == 0 {
		t.Skip("no topics yet")
	}
	s := topicTestServer(t)
	for _, prefix := range []string{"/en", ""} {
		code, body := topicGet(s, prefix+"/topics/")
		link := prefix + "/topic/" + all[0].Slug + "/"
		if code != 200 || !strings.Contains(body, `href="`+link+`"`) {
			t.Fatalf("%s/topics/: %d, links the first topic: %v", prefix, code, strings.Contains(body, link))
		}
		for _, tp := range all {
			path := prefix + "/topic/" + tp.Slug + "/"
			code, body := topicGet(s, path)
			if code != 200 {
				t.Fatalf("%s: %d", path, code)
			}
			c := tp.Lang(map[string]string{"/en": "en", "": "ar"}[prefix])
			canon := path
			for _, want := range []string{
				`<link rel="canonical" href="` + Base + canon + `">`,
				`hreflang="en" href="` + Base + "/en/topic/" + tp.Slug + `/"`,
				`hreflang="ar" href="` + Base + "/topic/" + tp.Slug + `/"`,
				`hreflang="x-default" href="` + Base + "/topic/" + tp.Slug + `/"`,
				`"dateModified":"` + tp.Updated + `"`,
				`"@type":"Article"`,
				`<blockquote lang="ar" dir="rtl">`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("%s lacks %s", path, want)
				}
			}
			if !strings.Contains(body, strings.ReplaceAll(c.Answer.Text, "'", "&#39;")) && !strings.Contains(body, c.Answer.Text) {
				t.Errorf("%s does not print its answer", path)
			}
			if strings.Contains(body, "FAQPage") {
				t.Errorf("%s: FAQPage markup is not a citation lever and is not used", path)
			}
			if strings.Contains(body, "\u2014") {
				t.Errorf("%s contains an em dash", path)
			}
		}
	}
	if code, _ := topicGet(s, "/topic/no-such-topic/"); code != 404 {
		t.Errorf("unknown topic: %d, want 404", code)
	}
}

func TestExplainedInPartial(t *testing.T) {
	s := topicTestServer(t)
	var b strings.Builder
	data := []TopicRef{{Slug: "a", Title: "Topic A"}, {Slug: "b", Title: "Topic B"}}
	if err := s.pages["article"].ExecuteTemplate(&b, "explained", data); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Explained in:", `href="/en/topic/a/"`, "Topic B"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("partial lacks %q: %s", want, b.String())
		}
	}
	b.Reset()
	if err := s.pagesAr["article"].ExecuteTemplate(&b, "explained", data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `href="/topic/a/"`) || !strings.Contains(b.String(), `dir="rtl"`) {
		t.Errorf("Arabic partial: %s", b.String())
	}
	b.Reset()
	s.pages["article"].ExecuteTemplate(&b, "explained", []TopicRef(nil))
	if strings.TrimSpace(b.String()) != "" {
		t.Errorf("no topics should print nothing: %q", b.String())
	}
}

func TestTopicSitemapPaths(t *testing.T) {
	paths := topicSitemapPaths()
	if paths[0] != "/topics/" || len(paths) != len(topics.All())+1 {
		t.Errorf("paths = %v", paths)
	}
}

func TestTopicDate(t *testing.T) {
	if got := topicDate("en", "2026-10-21"); got != "21 October 2026" {
		t.Errorf("en: %s", got)
	}
	if got := topicDate("ar", "2026-10-21"); got != "21 تشرين الأول 2026" {
		t.Errorf("ar: %s", got)
	}
}
