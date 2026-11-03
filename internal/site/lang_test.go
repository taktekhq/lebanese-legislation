package site

// The language layout: Arabic at the root, English under /en/, and /ar/ a
// 301 to the root. Each page kind is requested at the root and under /en/;
// the table says what the html tag, the canonical, the hreflang set and the
// language switcher must be.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var switchRe = regexp.MustCompile(`lang-switch" (?:dir="ltr" )?href="([^"]+)"`)

func TestLanguagePreferenceLayout(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/topics/", "/en/topics/"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		body := rec.Body.String()
		if rec.Code != 200 || !strings.Contains(body, `<script src="/language.js?v=`) {
			t.Fatalf("%s: status %d, missing language script", path, rec.Code)
		}
		hasNote := strings.Contains(body, `class="link lang-switch" href="/en/topics/" hreflang="en" lang="en" dir="ltr">Read this page in English</a>`)
		if hasNote != (path == "/topics/") {
			t.Errorf("%s: English note present=%v", path, hasNote)
		}
		if !switchRe.MatchString(body) {
			t.Errorf("%s: missing header language switch", path)
		}
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/language.js", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "lebleg-language") {
		t.Fatal("language script not served")
	}
	static, err := NewStatic(nil)
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	static.ServeHTTP(rec, httptest.NewRequest("GET", "/topics/", nil))
	if strings.Contains(rec.Body.String(), `<script src="/language.js`) {
		t.Fatal("static copies must not redirect to unavailable language routes")
	}
}

type langCase struct {
	name string
	path string // the root (Arabic) path, with the query if any
	pair bool   // a real translation pair: hreflang, and the English page is its own canonical
	// hidden: noindex in both languages (search, the rulings list), so
	// the pair names no hreflang: hreflang never points at a hidden page
	hidden bool
}

func langCases(t *testing.T, s *Server) []langCase {
	t.Helper()
	ctx := t.Context()
	cases := []langCase{
		{"home", "/", true, false}, {"laws list", "/laws/", true, false}, {"codes", "/codes/", true, false},
		{"rulings list", "/rulings/", true, true}, {"opinions page 2", "/opinions/page/2/", true, false},
		{"year list", "/laws/year/2005/", true, false}, {"data", "/data/", true, false}, {"about", "/about/", true, false},
		{"search", "/search/?q=tax", true, true},
	}
	one := func(name, sql string, f func(id int, num string) langCase) {
		var id int
		var num string
		if err := s.db.QueryRow(ctx, sql).Scan(&id, &num); err != nil {
			t.Logf("no %s in this database: %v", name, err)
			return
		}
		cases = append(cases, f(id, num))
	}
	// a text whose English is open (popular: noindex_page has no english row)
	one("law", `SELECT l.id, '' FROM legislation l WHERE NOT EXISTS (SELECT 1 FROM code c WHERE c.law_id = l.id)
		AND NOT EXISTS (SELECT 1 FROM noindex_page n WHERE n.kind = 'english' AND n.id = l.id)
		AND EXISTS (SELECT 1 FROM legislation_article a WHERE a.law_id = l.id) ORDER BY l.id LIMIT 1`,
		func(id int, _ string) langCase { return langCase{"law", lawPath(id), true, false} })
	one("article", `SELECT a.law_id, a.number FROM legislation_article a WHERE NOT EXISTS (SELECT 1 FROM code c WHERE c.law_id = a.law_id)
		AND NOT EXISTS (SELECT 1 FROM noindex_page n WHERE (n.kind = 'english' AND n.id = a.law_id) OR (n.kind = 'article' AND n.id = a.law_id AND n.position = a.position))
		ORDER BY a.law_id, a.position LIMIT 1`,
		func(id int, n string) langCase { return langCase{"article", artPath(id, n), true, false} })
	one("code", `SELECT law_id, '' FROM code ORDER BY position LIMIT 1`,
		func(id int, _ string) langCase { return langCase{"code", lawPath(id), true, false} })
	one("code hub", `SELECT 0, slug FROM code ORDER BY position LIMIT 1`,
		func(_ int, slug string) langCase { return langCase{"code hub", "/codes/" + slug + "/", true, false} })
	one("kind-year hub", `SELECT 2005, 'decree'`,
		func(y int, w string) langCase { return langCase{"kind-year hub", kindYearPath(y, w), true, false} })
	one("ruling", `SELECT id, '' FROM ruling ORDER BY id LIMIT 1`,
		func(id int, _ string) langCase {
			return langCase{"ruling", fmt.Sprintf("/ruling/%d/", id), false, false}
		})
	one("gazette", `SELECT id, '' FROM gazette_issue ORDER BY id LIMIT 1`,
		func(id int, _ string) langCase {
			return langCase{"gazette", fmt.Sprintf("/gazette/%d/", id), false, false}
		})
	one("opinion", `SELECT id, '' FROM opinion ORDER BY id LIMIT 1`,
		func(id int, _ string) langCase {
			return langCase{"opinion", fmt.Sprintf("/opinion/%d/", id), false, false}
		})
	return cases
}

func TestLanguageLayoutTable(t *testing.T) {
	s := testServer(t)
	cases := langCases(t, s)
	cases = append(cases, langCase{"topic", "/topic/" + firstTopicSlug(t) + "/", true, false})
	for _, c := range cases {
		path, query, _ := strings.Cut(c.path, "?")
		if query != "" {
			query = "?" + query
		}
		for _, v := range []struct {
			prefix, lang, dir string
		}{{"", "ar", "rtl"}, {"/en", "en", "ltr"}} {
			t.Run(c.name+" "+v.prefix, func(t *testing.T) {
				url := v.prefix + path + query
				rec := httptest.NewRecorder()
				s.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
				if rec.Code != 200 {
					t.Fatalf("GET %s: status %d (Location %q)", url, rec.Code, rec.Header().Get("Location"))
				}
				body := rec.Body.String()
				if want := fmt.Sprintf(`<html lang="%s" dir="%s">`, v.lang, v.dir); !strings.Contains(body, want) {
					t.Errorf("%s: missing %s", url, want)
				}
				// canonical: the root twin, except the English page of a pair
				canon := path
				if v.prefix == "/en" && c.pair {
					canon = "/en" + path
				}
				if got, _, hl := head(body); got != Base+canon {
					t.Errorf("%s: canonical %q, want %q", url, got, Base+canon)
				} else if hreflang := c.pair && !c.hidden; hreflang && hl != 3 || !hreflang && hl != 0 {
					t.Errorf("%s: %d hreflang links, pair=%v hidden=%v", url, hl, c.pair, c.hidden)
				}
				if c.pair && !c.hidden {
					assertHreflangPair(t, body, path, "/en"+path)
				}
				// the language switcher goes to the other interface
				want := Base + "/en" + path
				if v.prefix == "/en" {
					want = Base + path
				}
				if m := switchRe.FindStringSubmatch(body); m == nil || m[1] != want {
					t.Errorf("%s: switcher %v, want %s", url, m, want)
				}
				if strings.Contains(body, `data-href="/en/`) || strings.Contains(body, `data-href="/ar/`) {
					t.Errorf("%s: a citation copies a prefixed address, not the permanent root one", url)
				}
				if v.prefix == "/en" {
					assertInternalLinksUnderEn(t, body)
				} else {
					assertInternalLinksAreRoot(t, body, "/en"+path)
				}
			})
		}
	}
}

func firstTopicSlug(t *testing.T) string {
	t.Helper()
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/topics/", nil))
	m := regexp.MustCompile(`href="/topic/([^"/]+)/"`).FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Skip("no topics yet")
	}
	return m[1]
}

// /ar/... is a 301 to the same path at the root, query kept, for GET and HEAD,
// in one hop to wherever that path has moved (redirect.go): /ar/laws to /laws/,
// a text named with the wrong word to its own word.
func TestArPrefixRedirectsToRoot(t *testing.T) {
	s := testServer(t)
	for _, c := range []struct{ from, to string }{
		{"/ar", "/"}, {"/ar/", "/"}, {"/ar/laws/", "/laws/"}, {"/ar/laws", "/laws/"},
		{"/ar/search/?q=labour", "/search/?q=labour"}, {"/ar/law/244773/", lawPath(244773)},
		{"/ar/law/1/article/2/", lawPath(1) + "article/2/"}, {"/ar/ruling/5388/", "/ruling/5388/"},
		{"/ar/sitemaps/ar-laws-1.xml", "/sitemaps/ar-laws-1.xml"}, {"/ar/opinions/page/2/?x=1&y=2", "/opinions/page/2/?x=1&y=2"},
		{"/ar//evil.example/", "/evil.example/"},
	} {
		for _, method := range []string{"GET", "HEAD"} {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(method, c.from, nil))
			if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != c.to {
				t.Errorf("%s %s: %d Location %q, want 301 %q", method, c.from, rec.Code, rec.Header().Get("Location"), c.to)
			}
		}
	}
}

func TestRobotsAndLlmsUseTheNewPaths(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.robots(rec, httptest.NewRequest("GET", "/robots.txt", nil))
	body := rec.Body.String()
	for _, want := range []string{"Disallow: /search/", "Disallow: /en/search/", "Disallow: /en/changes/review/", "Sitemap: " + Base + "/sitemap.xml"} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt lacks %q", want)
		}
	}
	if strings.Contains(body, "/ar/") {
		t.Error("/ar/ must stay crawlable so crawlers see its redirect")
	}
}

func TestEnglishFormAndRedirectsStayUnderEn(t *testing.T) {
	s := testServer(t)
	// the go-to-article box and the wrong-word redirect keep the interface
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/en/laws", nil))
	if loc := rec.Header().Get("Location"); rec.Code/100 != 3 || loc != "/en/laws/" {
		t.Errorf("/en/laws: %d %q", rec.Code, loc)
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/en", nil))
	if loc := rec.Header().Get("Location"); rec.Code != 301 || loc != "/en/" {
		t.Errorf("/en: %d %q", rec.Code, loc)
	}
}

func TestNeutralPathsDoNotMoveUnderEn(t *testing.T) {
	s := testServer(t)
	for _, p := range []string{"/robots.txt", "/llms.txt", "/sitemap.xml", "/changes/feed.xml", "/styles.css", "/api/v1/"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code >= 300 && p != "/api/v1/" {
			t.Errorf("GET %s: %d", p, rec.Code)
		}
	}
}
