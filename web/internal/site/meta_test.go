package site

import (
	"context"
	"html"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/web/internal/site/i18n"
)

var (
	titleRe = regexp.MustCompile(`<title>([^<]*)</title>`)
	descRe  = regexp.MustCompile(`<meta name="description" content="([^"]*)">`)
	imgRe   = regexp.MustCompile(`(?i)<(?:img\b[^>]*\bsrc="http://|meta\b[^>]*(?:property="og:image(?::url)?"[^>]*content="http://|content="http://[^"]*"[^>]*property="og:image(?::url)?")|link\b[^>]*(?:rel="(?:icon|apple-touch-icon)"[^>]*href="http://|href="http://[^"]*"[^>]*rel="(?:icon|apple-touch-icon)"))`)
)

func checkAudit(t *testing.T, label, body string) {
	t.Helper()
	m := titleRe.FindStringSubmatch(body)
	if m == nil {
		t.Errorf("%s: no title", label)
	} else if n := len([]rune(html.UnescapeString(m[1]))); n > 60 {
		t.Errorf("%s: title is %d characters (max 60): %q", label, n, m[1])
	}
	m = descRe.FindStringSubmatch(body)
	if m == nil {
		t.Errorf("%s: no meta description", label)
	} else if n := len([]rune(html.UnescapeString(m[1]))); n < 70 || n > 160 {
		t.Errorf("%s: meta description is %d characters (want 70 to 160): %q", label, n, m[1])
	}
	if imgRe.MatchString(body) || strings.Contains(body, `"image":"http://`) {
		t.Errorf("%s: references an http:// image", label)
	}
}

func hreflangs(body string) map[string]string {
	got := map[string]string{}
	for _, m := range hreflangRe.FindAllStringSubmatch(body, -1) {
		got[m[1]] = m[2]
	}
	return got
}

func checkHreflangPair(t *testing.T, path string, arPage, enPage map[string]string, pair bool) {
	t.Helper()
	if !pair {
		if len(arPage) != 0 || len(enPage) != 0 {
			t.Errorf("%s: unexpected hreflang alternates: %v / %v", path, arPage, enPage)
		}
		return
	}
	if strings.Contains(path, "?") {
		path = strings.SplitN(path, "?", 2)[0]
	}
	want := map[string]string{"ar": Base + path, "en": Base + "/en" + path, "x-default": Base + path}
	for lang, href := range want {
		if arPage[lang] != href || enPage[lang] != href {
			t.Errorf("%s: hreflang %s is %q / %q, want %q on both pages", path, lang, arPage[lang], enPage[lang], href)
		}
	}
	if len(arPage) != len(want) || len(enPage) != len(want) {
		t.Errorf("%s: hreflang sets are incomplete: %v / %v", path, arPage, enPage)
	}
}

func TestFinishMetaLimits(t *testing.T) {
	long := strings.Repeat("كلمة ", 40)
	for _, b := range []struct {
		name   string
		bundle *i18n.Bundle
	}{{"en", en}, {"ar", ar}} {
		for _, c := range []struct{ title, desc string }{
			{"x", ""}, {"x", "Short."}, {long, long}, {"Law", strings.Repeat("a ", 69)}, {"Law", strings.Repeat("a", 70)},
		} {
			title, desc := finishMeta(b.bundle, c.title, c.desc)
			if n := len([]rune(title)); n > 60 {
				t.Errorf("%s: title %d characters", b.name, n)
			}
			if n := len([]rune(desc)); n < 70 || n > 160 {
				t.Errorf("%s: description %d characters for %q", b.name, n, c.desc)
			}
		}
	}
}

func TestEveryPageTemplateRendersAuditMetadataWithoutDatabase(t *testing.T) {
	oldBase := Base
	Base = "https://example.test"
	t.Cleanup(func() { Base = oldBase })
	s, err := newServer(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for name := range s.pages {
		path := "/about/"
		var alternates [2]map[string]string
		for i, lang := range []string{"ar", "en"} {
			t.Run(lang+"/"+name, func(t *testing.T) {
				r := httptest.NewRequest("GET", path, nil)
				r = r.WithContext(context.WithValue(r.Context(), langCtxKey, lang))
				p := Page{Title: "Audit page", Desc: "A rendered page used to check the site's search metadata output.", Path: path, Lang: "ar", Translated: true}
				if name == "codes" {
					p.Data = struct {
						Updated any
						Codes   []any
					}{}
				}
				if name == "review" {
					p.Data = struct{ Open []any }{}
				}
				body, err := s.execute(r, name, p)
				if err != nil {
					t.Fatalf("render %s: %v", name, err)
				}
				checkAudit(t, lang+"/"+name, body)
				alternates[i] = hreflangs(body)
			})
		}
		if name == "home" || name == "about" || name == "data" || name == "topics" || name == "topic" || name == "laws" || name == "codes" || name == "changes" {
			checkHreflangPair(t, path, alternates[0], alternates[1], true)
		}
	}
}

func TestImageAuditFindsHttpURLs(t *testing.T) {
	for _, body := range []string{
		`<img src="http://example.test/a.png">`,
		`<meta content="http://example.test/a.png" property="og:image">`,
		`<link href="http://example.test/favicon.ico" rel="icon">`,
	} {
		if !imgRe.MatchString(body) {
			t.Errorf("did not detect insecure image URL in %s", body)
		}
	}
}

func TestPagesMeetAuditWithDatabase(t *testing.T) {
	s := onpageServer(t)
	for _, c := range langCases(t, s) {
		root := strings.SplitN(c.path, "?", 2)[0]
		var pages [2]map[string]string
		for i, path := range []string{root, "/en" + root} {
			code, body, _ := getHTML(t, s, path)
			if code != 200 {
				t.Errorf("%s: status %d", path, code)
				continue
			}
			checkAudit(t, path, body)
			pages[i] = hreflangs(body)
		}
		checkHreflangPair(t, root, pages[0], pages[1], c.pair)
	}
}
