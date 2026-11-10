package site

import (
	"context"
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

func topicTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(nil) // the topic pages read the embedded files; law names fall back without a database
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

const googleOnlyNoIndex = `<meta name="googlebot" content="noindex, follow">`

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
		if !strings.Contains(body, googleOnlyNoIndex) || !strings.Contains(body, `<meta name="robots" content="index, follow, max-image-preview:large">`) {
			t.Errorf("%s/topics/ must be hidden from Google only", prefix)
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
				`<link rel="canonical" href="` + Base + canon + `">`, // self-canonical
				googleOnlyNoIndex, // generated: hidden from Google (#86), open to Bing (3 Nov)
				`<meta name="robots" content="index, follow, max-image-preview:large">`,
				`<link rel="alternate" hreflang="en" href="` + Base + "/en/topic/" + tp.Slug + `/">`,
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
	if _, body := topicGet(s, "/about/"); strings.Contains(body, `name="googlebot"`) {
		t.Error("/about/ carries a googlebot meta")
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

// The topic pages are not in the sitemaps: no addition to core.xml lists
// them. (The hubs need the database and are skipped here; the database test
// checks the files themselves, TestWeakPagesHidden.)
func TestTopicsNotInSitemap(t *testing.T) {
	for _, fn := range topicTestServer(t).coreExtra {
		urls := func() (urls []SitemapURL) {
			defer func() { recover() }() // no database
			urls, _ = fn(context.Background())
			return
		}()
		for _, u := range urls {
			if strings.HasPrefix(u.Path, "/topic") {
				t.Errorf("core sitemap lists %s", u.Path)
			}
		}
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

// A topic with a head title prints it as the whole <title>, uncut and without
// the site name; the H1 stays the question and lists keep the title. A topic
// without one keeps the title and the site name.
func TestTopicHeadTitle(t *testing.T) {
	if len(topics.All()) == 0 {
		t.Skip("no topics yet")
	}
	s := topicTestServer(t)
	withHead, without := 0, 0
	for _, tp := range topics.All() {
		for _, v := range []struct {
			prefix, lang string
			b            *i18n.Bundle
		}{{"", "ar", arBundle}, {"/en", "en", enBundle}} {
			c := tp.Lang(v.lang)
			code, body := topicGet(s, v.prefix+"/topic/"+tp.Slug+"/")
			if code != 200 {
				t.Fatalf("%s %s: status %d", tp.Slug, v.lang, code)
			}
			want := c.HeadTitle
			if want == "" {
				without++
				want, _ = finishMeta(v.b, c.Title+" · "+v.b.T("brand.name"), "")
			} else {
				withHead++
				if strings.Contains(want, "…") || len([]rune(want)) > titleMax {
					t.Errorf("%s %s: head title %q would be cut", tp.Slug, v.lang, want)
				}
			}
			if !strings.Contains(body, "<title>"+template.HTMLEscapeString(want)+"</title>") {
				t.Errorf("%s %s: <title> is not %q", tp.Slug, v.lang, want)
			}
			if !strings.Contains(body, ">"+template.HTMLEscapeString(c.Question)+"</h1>") {
				t.Errorf("%s %s: the H1 is not the question", tp.Slug, v.lang)
			}
		}
	}
	if withHead == 0 || without == 0 {
		t.Errorf("%d pages with a head title and %d without: the test needs both", withHead, without)
	}
}
