package site

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var switchRe = regexp.MustCompile(`lang-switch" (?:dir="ltr" )?href="([^"]+)"`)

type langCase struct {
	name string
	path string
	pair bool
}

func langCases(t *testing.T, s *Server) []langCase {
	t.Helper()
	ctx := t.Context()
	cases := []langCase{
		{"home", "/", true}, {"laws list", "/laws/", true}, {"codes", "/codes/", true},
		{"rulings list", "/rulings/", true}, {"opinions page 2", "/opinions/page/2/", true},
		{"year list", "/laws/year/2005/", true}, {"data", "/data/", true}, {"about", "/about/", true},
		{"search", "/search/?q=tax", true},
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
	one("law", `SELECT l.id, '' FROM legislation l WHERE NOT EXISTS (SELECT 1 FROM code c WHERE c.law_id = l.id)
		AND EXISTS (SELECT 1 FROM legislation_article a WHERE a.law_id = l.id) ORDER BY l.id LIMIT 1`,
		func(id int, _ string) langCase { return langCase{"law", lawPath(id), true} })
	one("article", `SELECT a.law_id, a.number FROM legislation_article a WHERE NOT EXISTS (SELECT 1 FROM code c WHERE c.law_id = a.law_id)
		ORDER BY a.law_id, a.position LIMIT 1`,
		func(id int, n string) langCase { return langCase{"article", artPath(id, n), true} })
	one("code", `SELECT law_id, '' FROM code ORDER BY position LIMIT 1`,
		func(id int, _ string) langCase { return langCase{"code", lawPath(id), true} })
	one("ruling", `SELECT id, '' FROM ruling ORDER BY id LIMIT 1`,
		func(id int, _ string) langCase { return langCase{"ruling", fmt.Sprintf("/ruling/%d/", id), false} })
	one("gazette", `SELECT id, '' FROM gazette_issue ORDER BY id LIMIT 1`,
		func(id int, _ string) langCase { return langCase{"gazette", fmt.Sprintf("/gazette/%d/", id), false} })
	one("opinion", `SELECT id, '' FROM opinion ORDER BY id LIMIT 1`,
		func(id int, _ string) langCase { return langCase{"opinion", fmt.Sprintf("/opinion/%d/", id), false} })
	return cases
}

func TestLanguageLayoutTable(t *testing.T) {
	s := testServer(t)
	cases := langCases(t, s)
	cases = append(cases, langCase{"topic", "/topic/" + firstTopicSlug(t) + "/", true})
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

				canon := path
				if v.prefix == "/en" && c.pair {
					canon = "/en" + path
				}
				if got, _, hl := head(body); got != Base+canon {
					t.Errorf("%s: canonical %q, want %q", url, got, Base+canon)
				} else if c.pair && hl != 3 || !c.pair && hl != 0 {
					t.Errorf("%s: %d hreflang links, pair=%v", url, hl, c.pair)
				}
				if c.pair {
					assertHreflangPair(t, body, path, "/en"+path)
				}

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

func TestArPostIsNotRedirected(t *testing.T) {
	s := testServer(t)
	for prefix, want := range map[string]string{"/ar": "/ai/?notify=bad#notify", "": "/ai/?notify=bad#notify", "/en": "/en/ai/?notify=bad#notify"} {
		r := httptest.NewRequest("POST", prefix+"/api/notify", strings.NewReader("email=a%40example.com"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, r)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
			t.Errorf("POST %s/api/notify: %d Location %q, want 303 %q", prefix, rec.Code, rec.Header().Get("Location"), want)
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
