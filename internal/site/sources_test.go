package site

import (
	"context"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
)

// sourcesServer serves the fictional fixture (DEMO_DATABASE_URL), as the
// serving job in checks.yml does; the test skips without it.
func sourcesServer(t *testing.T) *Server {
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

func sourcesGet(t *testing.T, s *Server, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != 200 {
		t.Fatalf("GET %s: %d", path, rec.Code)
	}
	return rec.Body.String()
}

// The sources page prints one table row per source with a link out, its date
// and an ItemList, in both languages.
func TestSourcesPage(t *testing.T) {
	s := sourcesServer(t)
	iso := sourcesChecked.Format("2006-01-02")
	for _, prefix := range []string{"/en", ""} {
		body := sourcesGet(t, s, prefix+"/sources/")
		if n := strings.Count(body, `<th scope="row">`); n != len(sourceRows) {
			t.Errorf("%s/sources/: %d table rows, want %d", prefix, n, len(sourceRows))
		}
		for _, row := range sourceRows {
			for _, l := range row.links {
				if !strings.Contains(body, `href="`+l.URL+`"`) {
					t.Errorf("%s/sources/: no link to %s", prefix, l.URL)
				}
			}
		}
		for _, want := range []string{"Legiliban", `"@type":"ItemList"`, `"dateModified":"` + iso + `"`, `<time datetime="` + iso + `">`,
			`"numberOfItems":13`, `href="` + prefix + `/legiliban/"`, `hreflang="en" href="` + Base + `/en/sources/"`, `hreflang="ar" href="` + Base + `/sources/"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s/sources/: missing %q", prefix, want)
			}
		}
		if strings.Contains(body, "sources.") {
			t.Errorf("%s/sources/: prints a string's key", prefix)
		}
	}
	en := sourcesGet(t, s, "/en/sources/")
	if !strings.Contains(en, "Not stated") || !strings.Contains(en, "not affiliated with any source") {
		t.Error("/en/sources/: missing the not-stated cells or the independence line")
	}
	if regexp.MustCompile(`(?i)\bbest\b`).MatchString(en[strings.Index(en, `<main`):strings.Index(en, `</main>`)]) {
		t.Error("/en/sources/ must not rank a source as best")
	}
}

// The Legiliban page states the source, the intention and, next to it, that
// the site is independent of the university.
func TestLegilibanPage(t *testing.T) {
	s := sourcesServer(t)
	iso := legilibanUpdated.Format("2006-01-02")
	en := sourcesGet(t, s, "/en/legiliban/")
	for _, want := range []string{
		"Legiliban is the main source of every text on this site.",
		"with the intention of giving it to the Lebanese University",
		"It is not affiliated with, endorsed by or operated by the Lebanese University or the Centre.",
		"The Official Gazette remains the authoritative text.",
		"not legal advice",
		`href="https://legallaw.ul.edu.lb/"`, "legiliban.ul.edu.lb", `href="/en/sources/"`,
		`"dateModified":"` + iso + `"`, `<time datetime="` + iso + `">`, `<html lang="en" dir="ltr">`,
	} {
		if !strings.Contains(en, want) {
			t.Errorf("/en/legiliban/: missing %q", want)
		}
	}
	// the intention and the independence sentence share one section
	i, j := strings.Index(en, "with the intention of giving it"), strings.Index(en, "It is not affiliated with")
	if i < 0 || j < i || strings.Contains(en[i:j], "<h2") {
		t.Error("/en/legiliban/: the independence sentence must follow the intention in the same section")
	}
	for _, banned := range []string{"partner", "agreement with", "permission", "in cooperation", "on behalf of"} {
		if strings.Contains(strings.ToLower(en[strings.Index(en, `<main`):strings.Index(en, `</main>`)]), banned) {
			t.Errorf("/en/legiliban/ must not imply a tie to the university: found %q", banned)
		}
	}
	ar := sourcesGet(t, s, "/legiliban/")
	for _, want := range []string{"Legiliban", `<html lang="ar" dir="rtl">`, `"dateModified":"` + iso + `"`, i18n.Arabic["legiliban.independent_p"]} {
		if !strings.Contains(ar, want) {
			t.Errorf("/legiliban/: missing %q", want)
		}
	}
}

// About and the home pages name Legiliban and link to the two pages; the core
// sitemap and llms.txt list them.
func TestSourcesLinkedAndListed(t *testing.T) {
	s := sourcesServer(t)
	for _, prefix := range []string{"/en", ""} {
		about := sourcesGet(t, s, prefix+"/about/")
		for _, want := range []string{"Legiliban", `href="` + prefix + `/legiliban/"`, `href="` + prefix + `/sources/"`} {
			if !strings.Contains(about, want) {
				t.Errorf("%s/about/: missing %q", prefix, want)
			}
		}
		home := sourcesGet(t, s, prefix+"/")
		if !strings.Contains(home, `class="tiny ls-hero__what"`) || !strings.Contains(home, `href="`+prefix+`/sources/"`) {
			t.Errorf("%s/: missing the sentence that says what the site is", prefix)
		}
	}
	home := sourcesGet(t, s, "/en/")
	main := home[strings.Index(home, `<main`):]
	text := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(main[:strings.Index(main, `ls-hero__what`)+400], " ")
	if i := strings.Index(text, "is a free online database of Lebanese law"); i < 0 || len(strings.Fields(text[:i])) > 120 {
		t.Errorf("/en/: the sentence is not within the first 120 words of the page's content")
	}
	for _, name := range []string{"/sitemaps/ar-pages.xml", "/sitemaps/pages.xml"} {
		xml := sourcesGet(t, s, name)
		prefix := "/en" // pages.xml lists the /en/ addresses, ar-pages.xml the root ones
		if strings.Contains(name, "ar-") {
			prefix = ""
		}
		for _, p := range []string{"/sources/", "/legiliban/"} {
			want := "<loc>" + Base + prefix + p + "</loc><lastmod>" + infoPageMods[p].Format("2006-01-02")
			if !strings.Contains(strings.Join(strings.Fields(xml), ""), want) {
				t.Errorf("%s: missing %s with its lastmod", name, prefix+p)
			}
		}
	}
	llms := sourcesGet(t, s, "/llms.txt")
	for _, want := range []string{Base + "/en/sources/", Base + "/en/legiliban/", "Legiliban"} {
		if !strings.Contains(llms, want) {
			t.Errorf("/llms.txt: missing %q", want)
		}
	}
}

// Every table cell is either a string in both languages or "not stated".
func TestSourceRowsHaveStrings(t *testing.T) {
	for _, row := range sourceRows {
		for _, k := range []string{"name", "price", "lang", "holds"} {
			key := "sources." + row.key + "." + k
			if i18n.English[key] == "" {
				t.Errorf("%s has no English string", key)
			}
		}
		for _, c := range sourceCols {
			key := "sources." + row.key + "." + c
			if i18n.English[key] != "" && i18n.Arabic[key] == "" {
				t.Errorf("%s has no Arabic string", key)
			}
		}
	}
	for _, table := range []map[string]string{i18n.English, i18n.Arabic} {
		for k, v := range table {
			if (strings.HasPrefix(k, "sources.") || strings.HasPrefix(k, "legiliban.")) && strings.ContainsAny(v, "\u2014\u2013") {
				t.Errorf("%s holds a dash", k)
			}
		}
	}
}
