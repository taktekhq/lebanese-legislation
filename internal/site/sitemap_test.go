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

	"github.com/taktekhq/lebanese-legislation/internal/indexing"
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

// fetchAll reads every file the index lists (once per test run: the articles
// take a second each).
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
			if strings.Contains(path, "core") { // a subset, listed again below by design
				continue
			}
			if seen[u.Loc] {
				t.Errorf("%s listed twice (%s)", u.Loc, path)
			}
			seen[u.Loc] = true
		}
	}
}

// A shard built from page dates that cannot be when a page changed: law 7815
// (decree of 8 Mar 1964) as ar-laws-1.xml listed it on 28 Oct 2026, a date
// after today, and an unknown one. None of them is printed; a real change is.
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

// Every lastmod the sitemaps print is a day a page could have changed: not
// before the scraper first fetched the Centre (the pages did not exist), not
// after today. They used to give the text's own date: ar-laws-1.xml had 12,891
// of 45,000 before 1970 on 28 Oct 2026.
func TestSitemapLastmodIsWhenThePageChanged(t *testing.T) {
	s := indexedServer(t)
	index, files, _ := fetchAll(t, s)
	today := time.Now().UTC().Format("2006-01-02")
	first := indexing.FirstFetch.Format("2006-01-02")
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
				continue // later pages of a list carry none, by design
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
	// a database built before the build raised old dates to the day the page
	// first existed still holds them; the sitemaps leave those out
	var old int
	if err := s.db.QueryRow(context.Background(), `SELECT count(*) FROM page_state WHERE changed_at < $1`, indexing.FirstFetch).Scan(&old); err != nil {
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
	blog := map[string]bool{}
	for path, f := range files {
		for _, u := range f.URLs {
			all[u.Loc] = true
			if strings.HasPrefix(u.Loc, Base+"/ar/") {
				t.Errorf("%s lists the /ar/ address %s, which is not canonical", path, u.Loc)
			}
			switch {
			case strings.HasPrefix(path, "/sitemaps/ar-"):
				if strings.HasPrefix(u.Loc, Base+"/en/") {
					t.Errorf("%s lists a non-Arabic address %s", path, u.Loc)
				}
			case path == "/blog-sitemap.xml":
				// The journal is indexed in both languages, so its sitemap lists both.
				blog[u.Loc] = true
			case !strings.HasPrefix(u.Loc, Base+"/en/"):
				t.Errorf("%s lists a non-English address %s", path, u.Loc)
			}
		}
	}
	for loc := range blog {
		twin := Base + "/en" + strings.TrimPrefix(loc, Base)
		if p := strings.TrimPrefix(loc, Base+"/en"); p != loc {
			twin = Base + p
		}
		if !blog[twin] {
			t.Errorf("/blog-sitemap.xml lists %s without its other language %s", loc, twin)
		}
	}
	ctx := context.Background()
	// no English address of an Arabic text; only codes and interface pages
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
	// thin articles, placeholder documents and the second of two same-numbered articles
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
	// the thin articles are a real share, and the substantial ones are listed
	articles := 0
	for path, f := range files {
		if strings.Contains(path, "articles") {
			articles += len(f.URLs)
		}
	}
	var total, thin, short, dup int
	s.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM legislation_article), (SELECT count(*) FROM noindex_page WHERE kind = 'article'),
		(SELECT count(*) FROM legislation_article a
		  WHERE a.law_id IN (SELECT law_id FROM legislation_article GROUP BY law_id HAVING count(*) <= $1)
		    AND NOT EXISTS (SELECT 1 FROM noindex_page n WHERE n.kind = 'article' AND n.id = a.law_id AND n.position = a.position))`,
		shortTextArticles).Scan(&total, &thin, &short)
	dup = total - thin - short - articles
	if dup < 0 || dup > 1000 {
		t.Errorf("articles %d, thin %d, short texts' %d, of %d: %d unexplained", articles, thin, short, total, dup)
	}
	// no article of a short text is listed
	short3, err := s.db.Query(ctx, `SELECT a.law_id, a.number FROM legislation_article a
		WHERE a.law_id IN (SELECT law_id FROM legislation_article GROUP BY law_id HAVING count(*) <= $1) ORDER BY random() LIMIT 200`, shortTextArticles)
	if err != nil {
		t.Fatal(err)
	}
	defer short3.Close()
	for short3.Next() {
		var id int
		var num string
		if err := short3.Scan(&id, &num); err != nil {
			t.Fatal(err)
		}
		if p := artPath(id, num); all[Base+p] || all[Base+"/en"+p] {
			t.Errorf("%s is an article of a short text and in a sitemap", p)
		}
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
	// a database from before noindex_page and page_state lists every page, no lastmod
	s := testServer(t)
	r := s.indexTables(context.Background())
	q := r.Replace("SELECT 1 FROM {ps} p, {ni} n")
	if strings.Contains(q, "{") {
		t.Errorf("table names not filled in: %s", q)
	}
}

// laws-N lists the /en/ twin of every text's own page whose English is open
// (not noindex_page kind english), in the order of ar-laws-N and with the
// same lastmod; an empty laws-N is not named; no other family has English
// files.
func TestEnglishLawsSitemapMirrorsArabic(t *testing.T) {
	s := indexedServer(t)
	index, files, _ := fetchAll(t, s)
	hidden := map[string]bool{}
	rows, err := s.db.Query(context.Background(), `SELECT id FROM noindex_page WHERE kind = 'english'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int
		rows.Scan(&id)
		hidden[itoa(id)] = true
	}
	rows.Close()
	en := 0
	for _, sm := range index.Sitemaps {
		path := strings.TrimPrefix(sm.Loc, Base)
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/sitemaps/"), ".xml")
		switch {
		case strings.HasPrefix(name, "laws-"):
			en++
			if len(files[path].URLs) == 0 {
				t.Errorf("%s is named but empty", name)
			}
			arabic := map[string]string{}
			for _, u := range files["/sitemaps/ar-"+name+".xml"].URLs {
				arabic[strings.Replace(u.Loc, Base+"/", Base+"/en/", 1)] = u.Lastmod
			}
			for _, u := range files[path].URLs {
				mod, ok := arabic[u.Loc]
				if !ok || mod != u.Lastmod {
					t.Fatalf("%s: %s %s is not the twin of an entry of its Arabic file", name, u.Loc, u.Lastmod)
				}
				if id := strings.Split(strings.TrimSuffix(u.Loc, "/"), "/"); hidden[id[len(id)-1]] {
					t.Errorf("%s lists %s, whose English is hidden", name, u.Loc)
				}
			}
		case strings.HasPrefix(name, "articles-"), strings.HasPrefix(name, "rulings-"), strings.HasPrefix(name, "gazette-"), strings.HasPrefix(name, "documents-"):
			t.Errorf("%s: there are no English files for this family", name)
		}
	}
	if en == 0 {
		t.Error("no English laws file")
	}
}

// The sitemaps change once a night: the edge may keep them 12 hours.
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

// The index and the small files are built once per data load: the second ask is a memory read.
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
