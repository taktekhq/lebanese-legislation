package site

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var hubArtRe = regexp.MustCompile(`href="(?:/en)?(/(?:law|decree|text)/\d+/article/[^"/]+/)"`)

// Every article of a code is a plain link on exactly one page of its hub, the
// pages are paths, self-canonical pairs, and the pager reaches them all.
func TestCodeHubLinksEveryArticle(t *testing.T) {
	s := indexedServer(t)
	ctx := context.Background()
	var slug string
	var id, total int
	if err := s.db.QueryRow(ctx, `SELECT c.slug, c.law_id, (SELECT count(*) FROM legislation_article a WHERE a.law_id = c.law_id)::int
		FROM code c ORDER BY 3 DESC LIMIT 1`).Scan(&slug, &id, &total); err != nil {
		t.Skip("no codes")
	}
	pages := (total + codeHubSize - 1) / codeHubSize
	seen := map[string]int{}
	for p := 1; p <= pages; p++ {
		path := codeHubPath(slug, p)
		resp, body := get(t, s, path)
		if resp.StatusCode != 200 || len(body) > 300_000 {
			t.Fatalf("%s: %d, %d bytes", path, resp.StatusCode, len(body))
		}
		if canon, robots, hl := head(body); canon != Base+path || !strings.HasPrefix(robots, "index") || hl != 3 {
			t.Errorf("%s: canonical %q robots %q hreflang %d", path, canon, robots, hl)
		}
		if strings.Contains(body, "?page=") {
			t.Errorf("%s uses ?page=", path)
		}
		links := hubArtRe.FindAllStringSubmatch(body, -1)
		if len(links) > codeHubSize {
			t.Errorf("%s lists %d articles", path, len(links))
		}
		for _, m := range links {
			seen[m[1]]++
		}
		if _, en := get(t, s, "/en"+path); !strings.Contains(en, `href="/en`+lawPath(id)+`article/`) {
			t.Errorf("/en%s does not link its articles under /en", path)
		}
	}
	if len(seen) != total {
		t.Errorf("code %s: %d articles linked from %d hub pages, want %d", slug, len(seen), pages, total)
	}
	for a, n := range seen {
		if n != 1 {
			t.Errorf("%s is on %d hub pages", a, n)
		}
	}
	if resp, _ := get(t, s, codeHubPath(slug, pages+1)); resp.StatusCode != 404 {
		t.Errorf("a page past the end: %d", resp.StatusCode)
	}
	if resp, _ := get(t, s, "/codes/no-such-code/"); resp.StatusCode != 404 {
		t.Errorf("an unknown code: %d", resp.StatusCode)
	}
	if resp, _ := get(t, s, "/codes/"+slug+"/?page=2"); resp.StatusCode != 301 && resp.StatusCode != 200 {
		t.Errorf("?page=2 on a code hub: %d", resp.StatusCode)
	}
}

// An article page links the hub page it is on, and that page has its anchor.
func TestArticleLinksItsCodeHubPage(t *testing.T) {
	s := indexedServer(t)
	ctx := context.Background()
	var slug, number string
	var id, pos int
	if err := s.db.QueryRow(ctx, `SELECT c.slug, c.law_id, a.number, a.position FROM code c
		JOIN legislation_article a ON a.law_id = c.law_id ORDER BY c.position, a.position OFFSET 150 LIMIT 1`).Scan(&slug, &id, &number, &pos); err != nil {
		t.Skip("no code with 150 articles")
	}
	var idx int
	if err := s.db.QueryRow(ctx, `SELECT count(*)::int FROM legislation_article WHERE law_id = $1 AND position < $2`, id, pos).Scan(&idx); err != nil {
		t.Fatal(err)
	}
	want := codeHubPath(slug, codeHubPage(idx)) + "#a-" + Slug(number)
	_, art := get(t, s, artPath(id, number))
	if !strings.Contains(art, `href="`+want+`"`) {
		t.Errorf("article page does not link %s", want)
	}
	_, hub := get(t, s, strings.SplitN(want, "#", 2)[0])
	if !strings.Contains(hub, `id="a-`+Slug(number)+`"`) {
		t.Errorf("hub page has no anchor for article %s", number)
	}
	_, law := get(t, s, lawPath(id))
	if !strings.Contains(law, `href="/codes/`+slug+`/"`) {
		t.Errorf("the code's text does not link its hub")
	}
}

// The kind hubs of a year add up to the year, list their texts under a plain
// path, and the year's own page links them.
func TestKindYearHubsDivideTheYear(t *testing.T) {
	s := indexedServer(t)
	ctx := context.Background()
	const year = 2005
	var yearTotal int
	if err := s.db.QueryRow(ctx, `SELECT count(*)::int FROM legislation WHERE issued_on >= '2005-01-01' AND issued_on < '2006-01-01'`).Scan(&yearTotal); err != nil || yearTotal == 0 {
		t.Skip("no 2005")
	}
	_, yearPage := get(t, s, "/laws/year/2005/")
	sum := 0
	for _, word := range kindYearWords {
		path := kindYearPath(year, word)
		resp, body := get(t, s, path)
		if resp.StatusCode == 404 {
			if strings.Contains(yearPage, `href="`+path+`"`) {
				t.Errorf("year page links %s, which is a 404", path)
			}
			continue
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		if !strings.Contains(yearPage, `href="`+path+`"`) {
			t.Errorf("year page does not link %s", path)
		}
		if canon, robots, hl := head(body); canon != Base+path || !strings.HasPrefix(robots, "index") || hl != 3 {
			t.Errorf("%s: canonical %q robots %q hreflang %d", path, canon, robots, hl)
		}
		var n int
		if err := s.db.QueryRow(ctx, `SELECT count(*)::int FROM legislation l WHERE l.issued_on >= '2005-01-01' AND l.issued_on < '2006-01-01' AND `+kindWordSQL+` = $1`, word).Scan(&n); err != nil {
			t.Fatal(err)
		}
		sum += n
		ids, pages := walkPages(t, s, path, 100)
		if len(ids) != n {
			t.Errorf("%s lists %d texts over %d pages, want %d", path, len(ids), len(pages), n)
		}
	}
	if sum != yearTotal {
		t.Errorf("the kind hubs of %d hold %d texts, the year %d", year, sum, yearTotal)
	}
	for _, p := range []string{"/laws/year/2005/nonsense/", "/laws/year/0/decree/", "/laws/year/1700/law/", "/laws/year/2005/page/"} {
		if resp, _ := get(t, s, p); resp.StatusCode != 404 {
			t.Errorf("%s: %d, want 404", p, resp.StatusCode)
		}
	}
}

// A text's page links the hub of its kind and year.
func TestLawPageLinksItsKindYearHub(t *testing.T) {
	s := indexedServer(t)
	var id int
	var kind string
	var year int
	if err := s.db.QueryRow(context.Background(), `SELECT l.id, l.kind, extract(year FROM l.issued_on)::int FROM legislation l
		WHERE l.issued_on IS NOT NULL AND l.kind LIKE 'مرسوم%' AND l.issued_on >= '2005-01-01' AND l.issued_on < '2006-01-01'
		ORDER BY l.id LIMIT 1`).Scan(&id, &kind, &year); err != nil {
		t.Skip("no decree of 2005")
	}
	_, body := get(t, s, lawPath(id))
	if want := fmt.Sprintf(`href="/laws/year/%d/decree/"`, year); !strings.Contains(body, want) {
		t.Errorf("decree page does not link %s", want)
	}
	_, en := get(t, s, "/en"+lawPath(id))
	if want := fmt.Sprintf(`href="/en/laws/year/%d/decree/"`, year); !strings.Contains(en, want) {
		t.Errorf("English decree page does not link %s", want)
	}
}

// Nothing that existed moved: the same address answers with the same status,
// and the redirects still go where they went.
func TestHubsLeaveEveryOldAddressAlone(t *testing.T) {
	s := indexedServer(t)
	ctx := context.Background()
	var slug string
	var id int
	if err := s.db.QueryRow(ctx, `SELECT slug, law_id FROM code ORDER BY position LIMIT 1`).Scan(&slug, &id); err != nil {
		t.Skip("no codes")
	}
	type want struct {
		path   string
		status int
		to     string
	}
	for _, w := range []want{
		{"/", 200, ""}, {"/codes/", 200, ""}, {"/laws/", 200, ""}, {"/laws/years/", 200, ""},
		{"/laws/year/2005/", 200, ""}, {"/laws/year/2005/page/2/", 301, "/laws/year/2005/"}, {"/laws/year/2005/page/1/", 301, "/laws/year/2005/"},
		{"/laws/year/2005/?page=2", 301, "/laws/year/2005/"},
		{"/rulings/year/2005/", 200, ""}, {"/gazette/years/", 200, ""}, {"/topics/", 200, ""},
		{"/code/" + slug + "/", 302, lawPath(id)},
		{lawPath(id), 200, ""}, {"/en" + lawPath(id), 200, ""},
		{"/law/1/", 404, ""},
	} {
		resp, _ := get(t, s, w.path)
		if resp.StatusCode != w.status {
			t.Errorf("%s: %d, want %d", w.path, resp.StatusCode, w.status)
			continue
		}
		if w.to != "" && strings.TrimPrefix(resp.Header.Get("Location"), "") != w.to {
			t.Errorf("%s redirects to %q, want %q", w.path, resp.Header.Get("Location"), w.to)
		}
	}
	var an string
	if err := s.db.QueryRow(ctx, `SELECT number FROM legislation_article WHERE law_id = $1 ORDER BY position LIMIT 1`, id).Scan(&an); err == nil {
		if resp, _ := get(t, s, artPath(id, an)); resp.StatusCode != 200 {
			t.Errorf("article %s: %d", artPath(id, an), resp.StatusCode)
		}
	}
}

// The hubs are listed in the core sitemap, in both languages, once each.
func TestHubsAreInTheCoreSitemap(t *testing.T) {
	s := indexedServer(t)
	rows, err := s.hubSitemapRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var codes, kinds int
	for _, r := range rows {
		if seen[r.Path] {
			t.Errorf("%s is listed twice", r.Path)
		}
		seen[r.Path] = true
		switch {
		case strings.HasPrefix(r.Path, "/codes/"):
			codes++
		case strings.HasPrefix(r.Path, "/laws/year/"):
			kinds++
		}
	}
	if codes == 0 || kinds == 0 {
		t.Fatalf("hub rows: %d code pages, %d kind-year pages", codes, kinds)
	}
	for _, p := range []string{"/laws/year/2005/decree/"} {
		if !seen[p] {
			t.Errorf("%s is not in the hub rows", p)
		}
	}
	_, core := get(t, s, "/sitemaps/core.xml")
	_, ar := get(t, s, "/sitemaps/ar-core.xml")
	if !strings.Contains(core, "/en/laws/year/2005/decree/") || !strings.Contains(ar, "<loc>"+Base+"/laws/year/2005/decree/</loc>") {
		t.Error("the core sitemaps do not list the kind-year hubs in both languages")
	}
}

// The years index links each year's list, not numbered pages: lists page by
// cursor (keyset.go), and an old numbered address moves to the year's first
// page.
func TestYearsIndexLinksYearsNotPages(t *testing.T) {
	s := indexedServer(t)
	for _, kind := range []string{"laws", "rulings", "gazette"} {
		path := "/" + kind + "/years/"
		resp, body := get(t, s, path)
		if resp.StatusCode != 200 || len(body) > 500_000 {
			t.Fatalf("%s: %d, %d bytes", path, resp.StatusCode, len(body))
		}
		if strings.Contains(body, "/page/") {
			t.Errorf("%s still links numbered pages", path)
		}
		ys, err := s.Years(context.Background(), kind)
		if err != nil {
			t.Fatal(err)
		}
		for _, y := range ys {
			if l := fmt.Sprintf(`href="/%s/year/%d/"`, kind, y.Year); !strings.Contains(body, l) {
				t.Errorf("%s does not link %s", path, l)
			}
		}
		if _, en := get(t, s, "/en"+path); len(ys) > 0 && !strings.Contains(en, `href="/en/`+kind+`/year/`) {
			t.Errorf("/en%s does not stay under /en", path)
		}
	}
	for old, want := range map[string]string{
		"/laws/year/2005/page/2/":       "/laws/year/2005/",
		"/en/rulings/year/2005/page/2/": "/en/rulings/year/2005/",
	} {
		resp, _ := get(t, s, old)
		if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != want {
			t.Errorf("%s: %d to %q, want 301 to %s", old, resp.StatusCode, resp.Header.Get("Location"), want)
		}
	}
}

// A text links the texts of its kind and year issued just before and after it,
// in the order its kind-and-year hub lists them.
func TestLawPageLinksItsNeighbours(t *testing.T) {
	s := indexedServer(t)
	ctx := context.Background()
	rows, err := s.db.Query(ctx, `SELECT l.id FROM legislation l WHERE l.issued_on >= '2005-01-01' AND l.issued_on < '2006-01-01'
		AND `+kindWordSQL+` = 'decree' ORDER BY l.issued_on DESC, l.id DESC LIMIT 5`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) < 3 {
		t.Skip("fewer than 3 decrees in 2005")
	}
	// hub order is newest first: ids[i-1] is the text after ids[i], ids[i+1] the text before
	mid := ids[1]
	_, body := get(t, s, lawPath(mid))
	for _, c := range []struct {
		rel string
		id  int
	}{{"prev", ids[2]}, {"next", ids[0]}} {
		if want := fmt.Sprintf(`rel="%s" href="%s"`, c.rel, lawPath(c.id)); !strings.Contains(body, want) {
			t.Errorf("%s does not link %s", lawPath(mid), want)
		}
	}
	// the newest decree of the year has no text after it in the year
	_, first := get(t, s, lawPath(ids[0]))
	if strings.Contains(first, `rel="next"`) {
		t.Errorf("%s links a text after it, but it is the newest of its kind in the year", lawPath(ids[0]))
	}
	if _, en := get(t, s, "/en"+lawPath(mid)); !strings.Contains(en, fmt.Sprintf(`rel="prev" href="/en%s"`, lawPath(ids[2]))) {
		t.Error("the English page does not link its neighbours under /en")
	}
}

func TestRobotsAllowsHubPaths(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.robots(rec, httptest.NewRequest("GET", "/robots.txt", nil))
	body := rec.Body.String()
	for _, p := range []string{"/codes/penal/", "/codes/penal/page/2/", "/laws/year/2005/decree/", "/laws/year/2005/decree/page/2/", "/laws/year/2005/page/2/"} {
		for _, line := range strings.Split(body, "\n") {
			if rule, ok := strings.CutPrefix(line, "Disallow: "); ok && rule != "" && strings.HasPrefix(p, rule) {
				t.Errorf("robots.txt blocks %s with %q", p, line)
			}
		}
	}
}
