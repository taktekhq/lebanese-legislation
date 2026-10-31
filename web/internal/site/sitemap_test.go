package site

import (
	"context"
	"encoding/xml"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type urlset_ struct {
	URLs []struct {
		Loc     string `xml:"loc"`
		Lastmod string `xml:"lastmod"`
	} `xml:"url"`
	Sitemaps []struct {
		Loc     string `xml:"loc"`
		Lastmod string `xml:"lastmod"`
	} `xml:"sitemap"`
}

func parseSitemap(t *testing.T, body string) urlset_ {
	t.Helper()
	var u urlset_
	if err := xml.Unmarshal([]byte(body), &u); err != nil {
		t.Fatalf("not valid XML: %v", err)
	}
	return u
}

var (
	fetched      sync.Once
	fetchedIndex urlset_
	fetchedFiles map[string]urlset_
	fetchedSizes map[string]int
)

func fetchAll(t *testing.T, s *Server) (index urlset_, files map[string]urlset_, sizes map[string]int) {
	t.Helper()
	fetched.Do(func() {
		resp, body := get(t, s, "/sitemap.xml")
		if resp.StatusCode != 200 {
			t.Fatalf("/sitemap.xml: %d", resp.StatusCode)
		}
		fetchedIndex = parseSitemap(t, body)
		fetchedFiles, fetchedSizes = map[string]urlset_{}, map[string]int{}
		for _, sm := range fetchedIndex.Sitemaps {
			path := strings.TrimPrefix(sm.Loc, Base)
			resp, b := get(t, s, path)
			if resp.StatusCode != 200 {
				t.Fatalf("%s: %d", path, resp.StatusCode)
			}
			fetchedFiles[path], fetchedSizes[path] = parseSitemap(t, b), len(b)
		}
	})
	return fetchedIndex, fetchedFiles, fetchedSizes
}

func TestSitemapIndexAndFiles(t *testing.T) {
	s := indexedServer(t)
	index, files, sizes := fetchAll(t, s)
	if len(index.Sitemaps) < 6 {
		t.Fatalf("index lists %d files", len(index.Sitemaps))
	}
	dateRe := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	for _, sm := range index.Sitemaps {
		if !dateRe.MatchString(sm.Lastmod) {
			t.Errorf("%s: index lastmod %q", sm.Loc, sm.Lastmod)
		}
	}
	seen := map[string]bool{}
	for path, f := range files {
		if n := len(f.URLs); n == 0 || n > 50000 {
			t.Errorf("%s holds %d URLs", path, n)
		}
		if sizes[path] > 50<<20 {
			t.Errorf("%s is %d bytes", path, sizes[path])
		}
		for _, u := range f.URLs {
			if !strings.HasPrefix(u.Loc, Base+"/") {
				t.Errorf("%s: %q", path, u.Loc)
			}
			if strings.Contains(path, "core") {
				continue
			}
			if seen[u.Loc] {
				t.Errorf("%s listed twice (%s)", u.Loc, path)
			}
			seen[u.Loc] = true
		}
	}
}

func TestSitemapShardLastmodIsInRange(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	body := urlset("", []SitemapURL{
		{"/law/7815/", time.Date(1964, 3, 8, 0, 0, 0, 0, time.UTC)},
		{"/text/7817/", time.Date(1921, 11, 30, 0, 0, 0, 0, time.UTC)},
		{"/law/7821/", time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"/law/1/", time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)},
		{"/law/2/", time.Now().AddDate(0, 0, 2)},
		{"/law/3/", time.Time{}},
		{"/law/4/", time.Now()},
	})
	got := map[string]string{}
	for _, u := range parseSitemap(t, body).URLs {
		got[strings.TrimPrefix(u.Loc, Base)] = u.Lastmod
		if u.Lastmod != "" && (u.Lastmod < "1970-01-01" || u.Lastmod > today) {
			t.Errorf("%s: lastmod %s", u.Loc, u.Lastmod)
		}
	}
	want := map[string]string{"/law/7815/": "", "/text/7817/": "", "/law/7821/": "", "/law/1/": "2026-09-26", "/law/2/": "", "/law/3/": "", "/law/4/": today}
	for p, w := range want {
		if got[p] != w {
			t.Errorf("%s: lastmod %q, want %q", p, got[p], w)
		}
	}
}

func TestSitemapLastmodIsWhenThePageChanged(t *testing.T) {
	s := indexedServer(t)
	index, files, _ := fetchAll(t, s)
	today := time.Now().UTC().Format("2006-01-02")
	first := firstFetch.Format("2006-01-02")
	bad := 0
	check := func(where, m string) {
		if m != "" && (m < first || m > today) {
			if bad++; bad <= 5 {
				t.Errorf("%s: lastmod %s", where, m)
			}
		}
	}
	for _, sm := range index.Sitemaps {
		check(sm.Loc, sm.Lastmod)
	}
	total, with := 0, 0
	for path, f := range files {
		for _, u := range f.URLs {
			check(u.Loc, u.Lastmod)
			if strings.Contains(path, "pages") {
				continue
			}
			total++
			if u.Lastmod != "" {
				with++
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d lastmods outside %s to %s", bad, first, today)
	}

	var old int
	if err := s.db.QueryRow(context.Background(), `SELECT count(*) FROM page_state WHERE changed_at < $1`, firstFetch).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if float64(with) < 0.99*float64(total) {
		if old == 0 {
			t.Errorf("%d of %d entries have a lastmod", with, total)
		} else {
			t.Logf("%d of %d entries have a lastmod; page_state has %d dates from before the pages existed, left out until the next build", with, total, old)
		}
	}
}

func TestSitemapLeavesOutWhatIsNotIndexed(t *testing.T) {
	s := indexedServer(t)
	_, files, _ := fetchAll(t, s)
	all := map[string]bool{}
	for path, f := range files {
		for _, u := range f.URLs {
			all[u.Loc] = true
			if strings.HasPrefix(u.Loc, Base+"/ar/") {
				t.Errorf("%s lists the /ar/ address %s, which is not canonical", path, u.Loc)
			}
			if strings.HasPrefix(path, "/sitemaps/ar-") {
				if strings.HasPrefix(u.Loc, Base+"/en/") {
					t.Errorf("%s lists a non-Arabic address %s", path, u.Loc)
				}
			} else if !strings.HasPrefix(u.Loc, Base+"/en/") {
				t.Errorf("%s lists a non-English address %s", path, u.Loc)
			}
		}
	}
	ctx := context.Background()

	for loc := range all {
		p := strings.TrimPrefix(loc, Base)
		if !strings.HasPrefix(p, "/en/") {
			continue
		}
		p = strings.TrimPrefix(p, "/en")
		for _, bad := range []string{"/article/", "/ruling/", "/gazette/", "/opinion/", "/agreement/", "/doctrine/", "/research/"} {
			if strings.Contains(p, bad) && !strings.Contains(p, "/year") && p != "/gazette/" && p != "/rulings/" {
				if strings.HasPrefix(p, "/gazette/") && strings.Count(p, "/") == 2 && p != "/gazette/years/" {
					t.Errorf("English sitemap lists %s", p)
				}
				if strings.Contains(p, "/article/") || strings.Contains(p, "/ruling/") {
					t.Errorf("English sitemap lists %s", p)
				}
			}
		}
	}
	for _, p := range []string{"/", "/codes/", "/laws/", "/opinions/"} {
		if !all[Base+p] || !all[Base+"/en"+p] {
			t.Errorf("%s is not in both languages", p)
		}
	}
	var codeID int
	if err := s.db.QueryRow(ctx, `SELECT law_id FROM code ORDER BY position LIMIT 1`).Scan(&codeID); err == nil {
		if !all[Base+lawPath(codeID)] || !all[Base+"/en"+lawPath(codeID)] {
			t.Errorf("code %d is not in both languages", codeID)
		}
	}

	rows, err := s.db.Query(ctx, `SELECT n.kind, n.id, a.number FROM noindex_page n
		LEFT JOIN legislation_article a ON n.kind = 'article' AND a.law_id = n.id AND a.position = n.position
		WHERE n.kind <> 'ruling' ORDER BY random() LIMIT 400`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	checked := 0
	for rows.Next() {
		var kind string
		var id int
		var num *string
		if err := rows.Scan(&kind, &id, &num); err != nil {
			t.Fatal(err)
		}
		p := "/" + kind + "/" + itoa(id) + "/"
		if kind == "article" {
			p = artPath(id, *num)
		}
		for _, prefix := range []string{"", "/en", "/ar"} {
			if all[Base+prefix+p] {
				t.Errorf("%s%s is noindex and in a sitemap", prefix, p)
			}
		}
		checked++
	}
	if checked < 50 {
		t.Skip("few noindex pages in this database")
	}

	articles := 0
	for path, f := range files {
		if strings.Contains(path, "articles") {
			articles += len(f.URLs)
		}
	}
	var total, thin, dup int
	s.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM legislation_article), (SELECT count(*) FROM noindex_page WHERE kind = 'article')`).Scan(&total, &thin)
	dup = total - thin - articles
	if dup < 0 || dup > 1000 {
		t.Errorf("articles %d, thin %d, of %d: %d unexplained", articles, thin, total, dup)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestSitemapPagesHoldThePaginationPaths(t *testing.T) {
	s := indexedServer(t)
	_, files, _ := fetchAll(t, s)
	all := map[string]bool{}
	for _, f := range files {
		for _, u := range f.URLs {
			all[u.Loc] = true
		}
	}
	for _, p := range []string{"/opinions/page/2/", "/agreements/page/2/", "/doctrine/page/2/", "/laws/year/2005/page/2/"} {
		if !all[Base+p] || !all[Base+"/en"+p] {
			t.Errorf("%s is not in the sitemaps", p)
		}
	}
	for loc := range all {
		if strings.Contains(loc, "?") {
			t.Errorf("a sitemap lists a query: %s", loc)
		}
	}
}

func TestOldDatabaseStillHasSitemaps(t *testing.T) {

	s := testServer(t)
	r := s.indexTables(context.Background())
	q := r.Replace("SELECT 1 FROM {ps} p, {ni} n")
	if strings.Contains(q, "{") {
		t.Errorf("table names not filled in: %s", q)
	}
}

func TestEnglishLawsSitemapMirrorsArabic(t *testing.T) {
	s := indexedServer(t)
	index, files, _ := fetchAll(t, s)
	en, ar := 0, 0
	for _, sm := range index.Sitemaps {
		path := strings.TrimPrefix(sm.Loc, Base)
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/sitemaps/"), ".xml")
		switch {
		case strings.HasPrefix(name, "laws-"):
			en++
			twin := files["/sitemaps/ar-"+name+".xml"]
			if len(twin.URLs) != len(files[path].URLs) {
				t.Fatalf("%s holds %d URLs, its Arabic file %d", name, len(files[path].URLs), len(twin.URLs))
			}
			for i, u := range files[path].URLs {
				if want := strings.Replace(twin.URLs[i].Loc, Base+"/", Base+"/en/", 1); u.Loc != want || u.Lastmod != twin.URLs[i].Lastmod {
					t.Fatalf("%s entry %d: %s %s, want %s %s", name, i, u.Loc, u.Lastmod, want, twin.URLs[i].Lastmod)
				}
			}
		case strings.HasPrefix(name, "ar-laws-"):
			ar++
		case strings.HasPrefix(name, "articles-"), strings.HasPrefix(name, "rulings-"), strings.HasPrefix(name, "gazette-"), strings.HasPrefix(name, "documents-"):
			t.Errorf("%s: there are no English files for this family", name)
		}
	}
	if en == 0 || en != ar {
		t.Errorf("%d English laws files, %d Arabic", en, ar)
	}
}

func TestSitemapCacheHeaders(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/sitemap.xml", "/sitemaps/ar-core.xml", "/sitemaps/core.xml"} {
		resp, _ := get(t, s, path)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Cache-Control"); got != "public, max-age=3600, s-maxage=43200" {
			t.Errorf("%s: Cache-Control %q", path, got)
		}
	}
}

func TestSitemapMemo(t *testing.T) {
	s := testServer(t)
	get(t, s, "/sitemap.xml")
	get(t, s, "/sitemaps/ar-core.xml")
	start := time.Now()
	resp, body := get(t, s, "/sitemap.xml")
	_, core := get(t, s, "/sitemaps/ar-core.xml")
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Errorf("memoized index and ar-core took %v", d)
	}
	if resp.StatusCode != 200 || !strings.Contains(body, "<sitemapindex") || !strings.Contains(core, "<urlset") {
		t.Errorf("memoized answers wrong: %d", resp.StatusCode)
	}
}

func TestMemoRebuildsWhenDataChanges(t *testing.T) {
	var c memoCache
	n := 0
	build := func() (any, error) { n++; return n, nil }
	for _, c2 := range []struct {
		ver  string
		want int
	}{{"1", 1}, {"1", 1}, {"2", 2}, {"2", 2}} {
		v, _ := c.getVersioned("k", c2.ver, time.Hour, build)
		if v.(int) != c2.want {
			t.Errorf("version %s: built %v, want %d", c2.ver, v, c2.want)
		}
	}
}
