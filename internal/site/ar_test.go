package site

// Tests for the two interfaces: Arabic at the root  and
// English under /en/. Every route renders with the right lang/dir, internal
// links stay in their interface, the hreflang pair is present. Like
// api_test.go, these skip rather than fail when the local Postgres isn't
// reachable.

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func getHTML(t *testing.T, s *Server, path string) (int, string, map[string]string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	resp := rec.Result()
	headers := map[string]string{}
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}
	return resp.StatusCode, rec.Body.String(), headers
}

// arPaths are representative pages across every kind of page the site
// serves, each as the root (Arabic) path; the test requests that path and its
// /en/ and /ar/ mirrors.
var arPaths = []string{
	"/", "/laws/", "/codes/", "/rulings/", "/gazette/", "/changes/", "/search/",
	"/data/", "/ai/", "/about/", "/opinions/", "/agreements/", "/doctrine/", "/research/",
	"/laws/years/", "/rulings/years/", "/gazette/years/",
}

func TestArabicRoutesRender(t *testing.T) {
	s := testServer(t)
	for _, p := range arPaths {
		t.Run(p, func(t *testing.T) {
			for _, prefix := range []string{""} {
				code, body, _ := getHTML(t, s, prefix+p)
				if code != 200 {
					t.Fatalf("GET %s%s: status %d", prefix, p, code)
				}
				if !strings.Contains(body, `<html lang="ar" dir="rtl">`) {
					t.Errorf("GET %s%s: missing <html lang=\"ar\" dir=\"rtl\">", prefix, p)
				}
			}
			code, body, _ := getHTML(t, s, "/en"+p)
			if code != 200 {
				t.Fatalf("GET /en%s: status %d", p, code)
			}
			if !strings.Contains(body, `<html lang="en" dir="ltr">`) {
				t.Errorf("GET /en%s: missing <html lang=\"en\" dir=\"ltr\">", p)
			}
		})
	}
}

func TestArabicLawAndArticlePages(t *testing.T) {
	s := testServer(t)
	var lawID int
	if err := s.db.QueryRow(t.Context(), `SELECT l.id FROM legislation l
		WHERE EXISTS (SELECT 1 FROM legislation_article a WHERE a.law_id = l.id) LIMIT 1`).Scan(&lawID); err != nil {
		t.Fatalf("pick a law: %v", err)
	}
	var number string
	if err := s.db.QueryRow(t.Context(), `SELECT number FROM legislation_article WHERE law_id = $1 ORDER BY position LIMIT 1`, lawID).Scan(&number); err != nil {
		t.Fatalf("pick an article: %v", err)
	}
	for _, prefix := range []string{""} {
		code, body, _ := getHTML(t, s, prefix+lawPath(lawID))
		if code != 200 {
			t.Fatalf("law page %s: status %d", prefix, code)
		}
		if !strings.Contains(body, `<html lang="ar" dir="rtl">`) {
			t.Fatalf("law page %s: missing lang/dir", prefix)
		}
		assertInternalLinksAreRoot(t, body, "/en"+lawPath(lawID))
		// a text's own page has an hreflang pair with its /en/ twin (TestLawPagesArePairs)

		code, body, _ = getHTML(t, s, prefix+artPath(lawID, number))
		if code != 200 {
			t.Fatalf("article page %s: status %d", prefix, code)
		}
		if !strings.Contains(body, `<html lang="ar" dir="rtl">`) {
			t.Fatalf("article page %s: missing lang/dir", prefix)
		}
		assertInternalLinksAreRoot(t, body, "/en"+artPath(lawID, number))
	}
	code, body, _ := getHTML(t, s, "/en"+lawPath(lawID))
	if code != 200 {
		t.Fatalf("English law page: status %d", code)
	}
	assertInternalLinksUnderEn(t, body)
}

var hrefRe = regexp.MustCompile(`\shref="(/[^"#]*)"`)

var anchorRe = regexp.MustCompile(`<a\s[^>]*>`)
var anchorAttrRe = regexp.MustCompile(`\s(href|lang)="([^"]*)"`)

// langTaggedLinks counts, per href, the anchors that declare their content
// language as lang. Multilingual search links an excerpt to the page holding
// that exact text, so such a link may cross interfaces. Counting occurrences
// keeps an untagged link to the same address an error.
func langTaggedLinks(body, lang string) map[string]int {
	out := map[string]int{}
	for _, tag := range anchorRe.FindAllString(body, -1) {
		var href, l string
		for _, m := range anchorAttrRe.FindAllStringSubmatch(tag, -1) {
			if m[1] == "href" {
				href = m[2]
			} else {
				l = m[2]
			}
		}
		if href != "" && l == lang {
			out[href]++
		}
	}
	return out
}

// neutralPath is a path shared by both interfaces by design: static files,
// feeds, scans, the API.
func neutralPath(p string) bool {
	for _, pre := range []string{"/assets/", "/styles", "/legislation", "/scan/", "/ruling-files/", "/api/", "/mcp", "/ph/", "/sitemap"} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return strings.HasSuffix(p, ".css") || strings.HasSuffix(p, ".js") || strings.HasSuffix(p, "feed.xml")
}

// assertInternalLinksAreRoot checks that every root-relative href on an Arabic
// page is a root (Arabic) address, other than the one link to the page's
// English version and links marked lang="en" (English content, such as a
// search excerpt that matched English text).
func assertInternalLinksAreRoot(t *testing.T, body, english string) {
	t.Helper()
	nEn := 0
	englishContent := langTaggedLinks(body, "en")
	for _, m := range hrefRe.FindAllStringSubmatch(body, -1) {
		p := m[1]
		switch {
		case neutralPath(p):
		case p == english:
			nEn++
		case strings.HasPrefix(p, "/en/") && englishContent[p] > 0:
			englishContent[p]--
		case strings.HasPrefix(p, "/ar/") || strings.HasPrefix(p, "/en/"):
			t.Errorf("internal link %q on an Arabic page is not a root address", p)
		}
	}
	if nEn != 1 {
		t.Errorf("an Arabic page has %d links to its English version %s, want 1 (at the top)", nEn, english)
	}
}

// assertInternalLinksUnderEn checks that every root-relative href on an
// English page stays under /en (the pages have no other way to be reached),
// except links marked lang="ar" to a root address (Arabic content).
func assertInternalLinksUnderEn(t *testing.T, body string) {
	t.Helper()
	arabic := langTaggedLinks(body, "ar")
	for _, m := range hrefRe.FindAllStringSubmatch(body, -1) {
		p := m[1]
		if !strings.HasPrefix(p, "/ar/") && arabic[p] > 0 {
			arabic[p]--
			continue
		}
		if !neutralPath(p) && !strings.HasPrefix(p, "/en/") && p != "/en" {
			t.Errorf("internal link %q on an English page does not stay under /en", p)
		}
	}
}

var hreflangRe = regexp.MustCompile(`hreflang="(en|ar|x-default)" href="([^"]+)"`)

func assertHreflangPair(t *testing.T, body, ar, en string) {
	t.Helper()
	found := map[string]string{}
	for _, m := range hreflangRe.FindAllStringSubmatch(body, -1) {
		found[m[1]] = m[2]
	}
	want := map[string]string{"ar": Base + ar, "en": Base + en, "x-default": Base + ar}
	for k, v := range want {
		if found[k] != v {
			t.Errorf("hreflang=%q is %q, want %q", k, found[k], v)
		}
	}
}

func TestLanguageSwitchLinks(t *testing.T) {
	s := testServer(t)
	_, enBody, _ := getHTML(t, s, "/en/laws/")
	if !strings.Contains(enBody, `href="`+Base+`/laws/" hreflang="ar"`) {
		t.Errorf("English page's switch link should go to the root twin")
	}
	for _, p := range []string{"/laws/"} {
		_, arBody, _ := getHTML(t, s, p)
		if !strings.Contains(arBody, `href="`+Base+`/en/laws/" hreflang="en"`) || !strings.Contains(arBody, ">English<") {
			t.Errorf("%s: the switch link should offer /en/laws/", p)
		}
	}
}

func TestArabicRedirectsStayInTheirPrefix(t *testing.T) {
	s := testServer(t)
	for prefix, want := range map[string]string{"/en": "/en/laws/", "": "/laws/"} {
		req := httptest.NewRequest("GET", prefix+"/laws", nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if loc := rec.Header().Get("Location"); loc != want {
			t.Errorf("GET %s/laws: Location = %q, want %s", prefix, loc, want)
		}
	}
}

func TestSitemapIndexHasArEntries(t *testing.T) {
	s := testServer(t)
	code, body, _ := getHTML(t, s, "/sitemap.xml")
	if code != 200 {
		t.Fatalf("sitemap.xml: status %d", code)
	}
	if !strings.Contains(body, "/sitemaps/ar-pages.xml") {
		t.Errorf("sitemap index missing ar-pages.xml")
	}
	if !strings.Contains(body, "/sitemaps/ar-laws-1.xml") {
		t.Errorf("sitemap index missing ar-laws-1.xml")
	}
}

func TestArabicSitemapURLsAreRoot(t *testing.T) {
	s := testServer(t)
	code, body, _ := getHTML(t, s, "/sitemaps/ar-laws-1.xml")
	if code != 200 {
		t.Fatalf("ar-laws-1.xml: status %d", code)
	}
	if !strings.Contains(body, "<loc>"+Base+"/law/") && !strings.Contains(body, "<loc>"+Base+"/decree/") {
		t.Errorf("ar-laws-1.xml URLs are not root addresses")
	}
	if strings.Contains(body, "/ar/") || strings.Contains(body, "/en/") {
		t.Errorf("ar-laws-1.xml lists a prefixed address")
	}
}

func TestLangTaggedLinksCountOnlyTaggedAnchors(t *testing.T) {
	body := `<a class="docrow" href="/en/topic/x/" lang="en" dir="auto">x</a><a href="/en/topic/x/">x</a><a lang="ar" href="/topic/y/">y</a>`
	if got := langTaggedLinks(body, "en"); got["/en/topic/x/"] != 1 || len(got) != 1 {
		t.Errorf("lang=en links: %v, want only /en/topic/x/ once", got)
	}
	if got := langTaggedLinks(body, "ar"); got["/topic/y/"] != 1 || len(got) != 1 {
		t.Errorf("lang=ar links: %v, want only /topic/y/ once", got)
	}
}
