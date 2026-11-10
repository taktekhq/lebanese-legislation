package site

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The French interface: /fr/ serves the site's own pages (frenchPaths) and
// sends every other address to its /en/ twin.

// language.js redirects a French reader only between pages that have a
// French version, so it carries the same list as lang.go.
func TestLanguageScriptKnowsTheFrenchPages(t *testing.T) {
	src, err := fs.ReadFile(StaticFiles(), "language.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`var french = (\[[^\]]*\]);`).FindSubmatch(src)
	if m == nil {
		t.Fatal("language.js has no french list")
	}
	var list []string
	if err := json.Unmarshal(m[1], &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != len(frenchPaths) {
		t.Errorf("language.js lists %d French pages, lang.go %d", len(list), len(frenchPaths))
	}
	for _, p := range list {
		if !frenchPaths[p] {
			t.Errorf("language.js lists %s, which lang.go does not", p)
		}
	}
}

// An address under /fr/ with no French version goes to its English page,
// query kept; no database is needed to say so.
func TestFrenchSendsOtherPagesToEnglish(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for from, to := range map[string]string{
		"/fr/law/12/":            "/en/law/12/",
		"/fr/law/12/article/3/":  "/en/law/12/article/3/",
		"/fr/ruling/7/":          "/en/ruling/7/",
		"/fr/topic/inheritance/": "/en/topic/inheritance/",
		"/fr/laws/year/2005/":    "/en/laws/year/2005/",
		"/fr/laws/page/2/?a=b":   "/en/laws/page/2/?a=b",
		"/fr/api/v1/law/1/":      "/en/api/v1/law/1/",
		"/fr":                    "/fr/",
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", from, nil))
		if loc := rec.Header().Get("Location"); rec.Code/100 != 3 || loc != to {
			t.Errorf("GET %s: %d to %q, want a redirect to %s", from, rec.Code, loc, to)
		}
	}
	for p, want := range map[string]string{"/law/1/": "/en/law/1/", "/about/": "/fr/about/", "/laws/?kind=x": "/fr/laws/?kind=x",
		"/contribute/#students": "/fr/contribute/#students", "/topic/rent/": "/en/topic/rent/", "/blog/": "/en/blog/"} {
		if got := frLink(p); got != want {
			t.Errorf("frLink(%s) = %s, want %s", p, got, want)
		}
	}
}

func frenchServer(t *testing.T) *Server {
	t.Helper()
	dsn := os.Getenv("DEMO_DATABASE_URL")
	if dsn == "" {
		t.Skip("DEMO_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Every French page renders in French, names itself as canonical, and links
// its Arabic and English twins; the twins name it back.
func TestFrenchPagesRender(t *testing.T) {
	s := frenchServer(t)
	get := func(p string) (int, string) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		return rec.Code, rec.Body.String()
	}
	served := 0
	for _, p := range FrenchPaths() {
		served++
		code, body := get("/fr" + p)
		if code != 200 {
			t.Errorf("GET /fr%s: %d", p, code)
			continue
		}
		for _, want := range []string{`<html lang="fr" dir="ltr">`, `<link rel="canonical" href="` + Base + `/fr` + p + `">`,
			`hreflang="en" lang="en">English</a>`, `hreflang="ar" lang="ar" dir="rtl">العربية</a>`, `Journal officiel`, `href="/fr/about/"`} {
			if !strings.Contains(body, want) {
				t.Errorf("/fr%s: missing %q", p, want)
			}
		}
		for _, english := range []string{">Legislation<", ">About<", "Skip to content", ">Search<", ">Project journal<", "Read this page in English"} {
			if strings.Contains(body, english) {
				t.Errorf("/fr%s: still says %q", p, english)
			}
		}
		noindex := strings.Contains(body, `<meta name="robots" content="noindex`)
		for _, alt := range []string{`hreflang="fr" href="` + Base + `/fr` + p + `"`, `hreflang="en" href="` + Base + `/en` + p + `"`, `hreflang="ar" href="` + Base + p + `"`, `hreflang="x-default" href="` + Base + p + `"`} {
			if strings.Contains(body, alt) == noindex {
				t.Errorf("/fr%s: hreflang %q present=%v on a page with noindex=%v", p, alt, !noindex, noindex)
			}
		}
		for _, twin := range []string{p, "/en" + p} {
			_, tb := get(twin)
			if strings.Contains(tb, `hreflang="fr" href="`+Base+`/fr`+p+`"`) == noindex {
				t.Errorf("%s: its link to the French page is wrong for noindex=%v", twin, noindex)
			}
			if !strings.Contains(tb, `href="/fr/" hreflang="fr" lang="fr">Français</a>`) {
				t.Errorf("%s: no French link in the language switch", twin)
			}
		}
	}
	if served != len(frenchPaths) {
		t.Errorf("%d of the %d French pages are served", served, len(frenchPaths))
	}
	// the Legiliban page keeps the independence sentence right after the intention
	_, leg := get("/fr/legiliban/")
	i, j := strings.Index(leg, "avec l'intention de le donner à l'Université libanaise"), strings.Index(leg, "Il n'est ni affilié à l'Université libanaise ou au Centre, ni approuvé par eux, ni exploité par eux.")
	if i < 0 || j < i || strings.Contains(leg[i:j], "<h2") {
		t.Error("/fr/legiliban/: the independence sentence must follow the intention in the same section")
	}
	for _, want := range []string{"Legiliban", `"dateModified":"` + legilibanUpdated.Format("2006-01-02") + `"`, `href="/fr/sources/"`, "Une interface en anglais, en arabe et en français."} {
		if !strings.Contains(leg, want) {
			t.Errorf("/fr/legiliban/: missing %q", want)
		}
	}
	_, src := get("/fr/sources/")
	if n := strings.Count(src, `<th scope="row">`); n != len(sourceRows) || !strings.Contains(src, "Non précisé") || !strings.Contains(src, `"@type":"ItemList"`) || strings.Contains(src, "Not stated") {
		t.Errorf("/fr/sources/: %d rows, or the table is not in French", n)
	}
	for _, p := range []string{"/fr/", "/fr/about/"} {
		if _, body := get(p); !strings.Contains(body, `href="/fr/sources/"`) || !strings.Contains(body, `href="/fr/legiliban/"`) {
			t.Errorf("%s: no French links to the sources and Legiliban pages", p)
		}
	}
	if _, home := get("/fr/"); !strings.Contains(home, "est une base de données en ligne gratuite du droit libanais") {
		t.Error("/fr/: missing the sentence that says what the site is")
	}
	if _, en := get("/en/legiliban/"); !strings.Contains(en, "An interface in English, Arabic and French.") || strings.Contains(en, "soon French") {
		t.Error("/en/legiliban/ must name three interface languages")
	}
	// a text's page keeps its two languages and names no French twin
	if _, body := get("/en/law/1/"); strings.Contains(body, `hreflang="fr" href=`) {
		t.Error("/en/law/1/ names a French version it does not have")
	}
	// French pages link a text to its English page
	if _, body := get("/fr/"); !strings.Contains(body, `href="/en/law/1/"`) || strings.Contains(body, `href="/fr/law/`) {
		t.Error("/fr/ must link texts to their /en/ pages")
	}
	_, xml := get("/sitemaps/fr-pages.xml")
	for _, want := range []string{"<loc>" + Base + "/fr/</loc>", "<loc>" + Base + "/fr/about/</loc>", "<loc>" + Base + "/fr/laws/</loc>",
		"<loc>" + Base + "/fr/sources/</loc><lastmod>" + sourcesChecked.Format("2006-01-02"), "<loc>" + Base + "/fr/legiliban/</loc><lastmod>" + legilibanUpdated.Format("2006-01-02")} {
		if !strings.Contains(xml, want) {
			t.Errorf("fr-pages.xml: missing %s", want)
		}
	}
	for _, hidden := range []string{"/fr/search/", "/fr/rulings/", "/fr/topics/", "/fr/law/"} {
		if strings.Contains(xml, Base+hidden) {
			t.Errorf("fr-pages.xml lists %s", hidden)
		}
	}
	if _, idx := get("/sitemap.xml"); !strings.Contains(idx, Base+"/sitemaps/fr-pages.xml") {
		t.Error("the sitemap index does not name fr-pages.xml")
	}
}
