package site

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

func neutralPath(p string) bool {
	for _, pre := range []string{"/assets/", "/styles", "/legislation", "/scan/", "/ruling-files/", "/api/", "/mcp", "/ph/", "/sitemap"} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return strings.HasSuffix(p, ".css") || strings.HasSuffix(p, ".js") || strings.HasSuffix(p, "feed.xml")
}

func assertInternalLinksAreRoot(t *testing.T, body, english string) {
	t.Helper()
	nEn := 0
	for _, m := range hrefRe.FindAllStringSubmatch(body, -1) {
		p := m[1]
		switch {
		case neutralPath(p):
		case p == english:
			nEn++
		case strings.HasPrefix(p, "/ar/") || strings.HasPrefix(p, "/en/"):
			t.Errorf("internal link %q on an Arabic page is not a root address", p)
		}
	}
	if nEn != 1 {
		t.Errorf("an Arabic page has %d links to its English version %s, want 1 (at the top)", nEn, english)
	}
}

func assertInternalLinksUnderEn(t *testing.T, body string) {
	t.Helper()
	for _, m := range hrefRe.FindAllStringSubmatch(body, -1) {
		if p := m[1]; !neutralPath(p) && !strings.HasPrefix(p, "/en/") && p != "/en" {
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
