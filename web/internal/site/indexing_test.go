package site

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestPagePath(t *testing.T) {
	for _, c := range []struct {
		base string
		n    int
		want string
	}{{"/opinions/", 1, "/opinions/"}, {"/opinions/", 2, "/opinions/page/2/"},
		{"/laws/year/2020/", 7, "/laws/year/2020/page/7/"}, {"/opinions/", 0, "/opinions/"}} {
		if got := pagePath(c.base, c.n); got != c.want {
			t.Errorf("pagePath(%q, %d) = %q, want %q", c.base, c.n, got, c.want)
		}
		if strings.Contains(pagePath(c.base, c.n), "?") {
			t.Errorf("a page address has a query: robots.txt blocks /*?")
		}
	}
}

func TestPageNums(t *testing.T) {
	if got := pageNums(1, 1); got != nil {
		t.Errorf("one page has no pager: %v", got)
	}

	if got := pageNums(3, 9); len(got) != 9 || got[0] != 1 || got[8] != 9 {
		t.Errorf("9 pages: %v", got)
	}

	got := pageNums(37, 91)
	has := map[int]bool{}
	for _, n := range got {
		has[n] = true
	}
	for _, n := range []int{1, 10, 20, 30, 32, 37, 42, 50, 90, 91, 0} {
		if !has[n] {
			t.Errorf("page 37 of 91 lacks %d: %v", n, got)
		}
	}

	big := pageNums(1200, 2578)
	if len(big) > 90 {
		t.Errorf("2,578 pages made %d links", len(big))
	}
	for p := 1; p <= 2578; p++ {
		reach := map[int]bool{}
		for _, n := range pageNums(p, 2578) {
			reach[n] = true
		}

		if !reach[1] || !reach[2578] {
			t.Fatalf("page %d does not link the first and last pages", p)
		}
	}

	for _, n := range [][]int{got, big} {
		if n[0] == 0 || n[len(n)-1] == 0 {
			t.Errorf("gap at an end: %v", n)
		}
		for i := 1; i < len(n); i++ {
			if n[i] == 0 && n[i-1] == 0 {
				t.Errorf("two gaps in a row: %v", n)
			}
		}
	}
}

func TestEveryPageIsAFewClicksAway(t *testing.T) {
	for _, pages := range []int{91, 250, 2578} {
		maxHops := 2
		if pages > 300 {
			maxHops = 3
		}
		dist := map[int]int{1: 0}
		frontier := []int{1}
		for hop := 1; hop <= maxHops; hop++ {
			var next []int
			for _, p := range frontier {
				for _, n := range pageNums(p, pages) {
					if n != 0 {
						if _, ok := dist[n]; !ok {
							dist[n] = hop
							next = append(next, n)
						}
					}
				}
			}
			frontier = next
		}
		if len(dist) != pages {
			t.Errorf("%d pages: %d reachable in %d clicks", pages, len(dist), maxHops)
		}
	}
}

func req(lang string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	return r.WithContext(context.WithValue(r.Context(), langCtxKey, lang))
}

func TestCanonicalRule(t *testing.T) {
	s := &Server{}
	for _, c := range []struct {
		name  string
		lang  string
		page  Page
		canon string
		pair  bool
	}{
		{"English page with Arabic text points to the root address", "en", Page{Lang: "ar", Path: "/decree/193806/"}, "/decree/193806/", false},
		{"the Arabic address is its own canonical", "ar", Page{Lang: "ar", Path: "/decree/193806/"}, "/decree/193806/", false},
		{"a code with English content is its own canonical in English", "en", Page{Lang: "ar", Translated: true, Path: "/law/244058/"}, "/en/law/244058/", true},
		{"and in Arabic", "ar", Page{Lang: "ar", Translated: true, Path: "/law/244058/"}, "/law/244058/", true},
		{"an interface page is a pair", "en", Page{Path: "/laws/"}, "/en/laws/", true},
		{"and its Arabic version", "ar", Page{Path: "/laws/"}, "/laws/", true},
		{"a later page of a list", "en", Page{Path: "/opinions/page/2/"}, "/en/opinions/page/2/", true},
		{"an article is Arabic text", "en", Page{Lang: "ar", Path: "/law/1/article/2/"}, "/law/1/article/2/", false},
		{"a part of a long text is Arabic text", "en", Page{Lang: "ar", Path: "/law/244736/part/2/"}, "/law/244736/part/2/", false},
	} {
		canon, pair := s.canonical(req(c.lang), c.page)
		if canon != c.canon || pair != c.pair {
			t.Errorf("%s: got %q pair=%v, want %q pair=%v", c.name, canon, pair, c.canon, c.pair)
		}
	}
	static := &Server{isStatic: true}
	if canon, pair := static.canonical(req("en"), Page{Lang: "ar", Path: "/law/1/"}); canon != "/law/1/" || pair {
		t.Errorf("a static copy has no Arabic pages: %q %v", canon, pair)
	}
}

func TestSplitItems(t *testing.T) {

	sizes := []int{300, 300, 300, chapterBytes, 300, 300}
	chapter := []bool{false, false, false, true, false, false}
	for _, run := range splitItems(sizes, chapter, 650) {
		if chapter[run[1]-1] {
			t.Errorf("a run ends with a chapter heading: %v", run)
		}
	}

	runs := splitItems([]int{500, chapterBytes, 500}, []bool{false, true, false}, 600)
	if len(runs) != 2 || runs[0] != [2]int{0, 1} || runs[1] != [2]int{1, 3} {
		t.Errorf("chapter stranded: %v", runs)
	}

	runs = splitItems([]int{2000, 10, 10}, []bool{false, false, false}, 100)
	total := 0
	for _, r := range runs {
		total += r[1] - r[0]
	}
	if total != 3 || runs[0] != [2]int{0, 1} {
		t.Errorf("runs = %v", runs)
	}

	for i := 1; i < len(runs); i++ {
		if runs[i][0] != runs[i-1][1] {
			t.Errorf("gap between runs: %v", runs)
		}
	}
	if b := budgetFor(1_000_000); b >= partBudget || b < 500_000 {
		t.Errorf("1 MB in two even parts, budget %d", b)
	}
}

func TestSectionPartPath(t *testing.T) {
	l := &Law{Part: 2, SectionPart: map[int]int{10: 1, 11: 2, 12: 3}}
	l.ID = 5
	for id, want := range map[int]string{10: "/law/5/", 11: "", 12: "/law/5/part/3/", 99: ""} {
		if got := l.SectionPartPath(id); got != want {
			t.Errorf("chapter %d: %q, want %q", id, got, want)
		}
	}
}

func get(t *testing.T, s *Server, path string) (*http.Response, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	resp := rec.Result()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func indexedServer(t *testing.T) *Server {
	t.Helper()
	s := testServer(t)
	var n int
	if err := s.db.QueryRow(context.Background(), `SELECT count(*) FROM page_state`).Scan(&n); err != nil || n == 0 {
		t.Skip("this database has no page_state: run lebleg build -only indexing")
	}
	return s
}

var (
	canonRe  = regexp.MustCompile(`<link rel="canonical" href="([^"]+)"`)
	robotsRe = regexp.MustCompile(`<meta name="robots" content="([^"]+)"`)
)

func head(body string) (canon, robots string, hreflang int) {
	if m := canonRe.FindStringSubmatch(body); m != nil {
		canon = m[1]
	}
	if m := robotsRe.FindStringSubmatch(body); m != nil {
		robots = m[1]
	}
	return canon, robots, strings.Count(body, `rel="alternate" hreflang=`)
}

func TestLawPagesArePairs(t *testing.T) {
	s := indexedServer(t)
	_, en := get(t, s, "/en/decree/193806/")
	canon, robots, hl := head(en)
	if canon != Base+"/en/decree/193806/" || hl != 3 || !strings.HasPrefix(robots, "index") {
		t.Errorf("English decree page: canonical %q, %d hreflang, robots %q", canon, hl, robots)
	}
	for _, h := range []string{`hreflang="en" href="` + Base + `/en/decree/193806/"`, `hreflang="ar" href="` + Base + `/decree/193806/"`, `hreflang="x-default" href="` + Base + `/decree/193806/"`} {
		if !strings.Contains(en, h) {
			t.Errorf("English decree page lacks %s", h)
		}
	}
	_, ar := get(t, s, "/decree/193806/")
	if canon, _, hl := head(ar); canon != Base+"/decree/193806/" || hl != 3 {
		t.Errorf("Arabic decree page: canonical %q, %d hreflang", canon, hl)
	}
	if !strings.Contains(ar, `<html lang="ar" dir="rtl">`) {
		t.Error("/decree/193806/ is not the Arabic interface")
	}
	if !strings.Contains(en, `<html lang="en"`) || !strings.Contains(en, "Legislation") {
		t.Error("the English page lost its English interface")
	}
}

func TestArticlePagesArePairs(t *testing.T) {
	s := indexedServer(t)
	_, en := get(t, s, "/en/decree/193806/article/1/")
	if canon, _, hl := head(en); canon != Base+"/en/decree/193806/article/1/" || hl != 3 {
		t.Errorf("English article page: canonical %q, %d hreflang", canon, hl)
	}
	assertHreflangPair(t, en, "/decree/193806/article/1/", "/en/decree/193806/article/1/")
	_, ar := get(t, s, "/decree/193806/article/1/")
	if canon, _, hl := head(ar); canon != Base+"/decree/193806/article/1/" || hl != 3 {
		t.Errorf("Arabic article page: canonical %q, %d hreflang", canon, hl)
	}
	assertHreflangPair(t, ar, "/decree/193806/article/1/", "/en/decree/193806/article/1/")
}

func TestCodeKeepsItsEnglishPair(t *testing.T) {
	s := indexedServer(t)
	var id int
	if err := s.db.QueryRow(context.Background(), `SELECT law_id FROM code ORDER BY position LIMIT 1`).Scan(&id); err != nil {
		t.Skip("no codes")
	}
	_, en := get(t, s, "/en"+lawPath(id))
	canon, _, hl := head(en)
	if canon != Base+"/en"+lawPath(id) || hl != 3 {
		t.Errorf("code page: canonical %q, %d hreflang (want itself and 3)", canon, hl)
	}
	for _, p := range []string{lawPath(id)} {
		_, ar := get(t, s, p)
		if canon, _, hl := head(ar); canon != Base+lawPath(id) || hl != 3 {
			t.Errorf("Arabic code page %s: canonical %q, %d hreflang", p, canon, hl)
		}
	}
}

func TestInterfacePagesStayPairs(t *testing.T) {
	s := indexedServer(t)
	for _, p := range []string{"/", "/laws/", "/opinions/", "/laws/years/", "/laws/year/2005/"} {
		_, body := get(t, s, p)
		if canon, _, hl := head(body); canon != Base+p || hl != 3 {
			t.Errorf("%s: canonical %q, %d hreflang", p, canon, hl)
		}
		_, body = get(t, s, "/en"+p)
		if canon, _, hl := head(body); canon != Base+"/en"+p || hl != 3 {
			t.Errorf("/en%s: canonical %q, %d hreflang", p, canon, hl)
		}
	}
}

func TestThinPagesAreNoindexFollow(t *testing.T) {
	s := indexedServer(t)
	ctx := context.Background()
	var law, pos int
	var num string
	err := s.db.QueryRow(ctx, `SELECT n.id, n.position, a.number FROM noindex_page n
		JOIN legislation_article a ON a.law_id = n.id AND a.position = n.position
		WHERE n.kind = 'article' AND n.reason = 'boilerplate' ORDER BY n.id LIMIT 1`).Scan(&law, &pos, &num)
	if err != nil {
		t.Skip("no thin article in this database")
	}
	resp, body := get(t, s, artPath(law, num))
	_, robots, _ := head(body)
	if resp.StatusCode != 200 || robots != "noindex, follow" {
		t.Errorf("thin article %d/%s: status %d robots %q", law, num, resp.StatusCode, robots)
	}

	if !strings.Contains(body, lawPath(law)) {
		t.Error("a thin article does not link to its text")
	}

	var good int
	var gnum string
	if err := s.db.QueryRow(ctx, `SELECT a.law_id, a.number FROM legislation_article a
		WHERE length(a.text_ar) > 1500 AND NOT EXISTS (SELECT 1 FROM noindex_page n WHERE n.kind = 'article' AND n.id = a.law_id AND n.position = a.position)
		ORDER BY a.law_id LIMIT 1`).Scan(&good, &gnum); err == nil {
		_, body := get(t, s, artPath(good, gnum))
		if _, robots, _ := head(body); !strings.HasPrefix(robots, "index") {
			t.Errorf("a long article %d/%s is %q", good, gnum, robots)
		}
	}
}

func TestPlaceholderDocumentsAreNoindex(t *testing.T) {
	s := indexedServer(t)
	for _, c := range []struct{ kind, path string }{{"agreement", "/agreement/%d/"}, {"doctrine", "/doctrine/%d/"}} {
		var id int
		if err := s.db.QueryRow(context.Background(), `SELECT id FROM noindex_page WHERE kind = $1 AND reason = 'placeholder' ORDER BY id LIMIT 1`, c.kind).Scan(&id); err != nil {
			t.Skipf("no placeholder %s", c.kind)
		}
		_, body := get(t, s, strings.Replace(c.path, "%d", strconv.Itoa(id), 1))
		if _, robots, _ := head(body); robots != "noindex, follow" {
			t.Errorf("placeholder %s %d: robots %q", c.kind, id, robots)
		}
	}
}

func TestOldPageQueryRedirects(t *testing.T) {
	s := indexedServer(t)
	for _, c := range []struct{ from, to string }{
		{"/opinions/?page=2", "/opinions/page/2/"},
		{"/agreements/?page=3", "/agreements/page/3/"},
		{"/doctrine/?page=1", "/doctrine/"},
		{"/opinions/page/1/", "/opinions/"},
		{"/en/opinions/?page=2", "/en/opinions/page/2/"},
		{"/en/opinions/page/1/", "/en/opinions/"},
		{"/en/laws/year/2005/page/1/", "/en/laws/year/2005/"},
		{"/laws/year/2005/?page=2", "/laws/year/2005/page/2/"},
		{"/laws/year/2005/page/1/", "/laws/year/2005/"},
	} {
		resp, _ := get(t, s, c.from)
		if resp.StatusCode != 301 || resp.Header.Get("Location") != c.to {
			t.Errorf("%s: %d %q, want 301 %q", c.from, resp.StatusCode, resp.Header.Get("Location"), c.to)
		}
	}
	for _, p := range []string{"/opinions/page/9999/", "/opinions/page/0/", "/opinions/page/x/", "/laws/year/2005/page/99/"} {
		if resp, _ := get(t, s, p); resp.StatusCode != 404 {
			t.Errorf("%s: %d, want 404", p, resp.StatusCode)
		}
	}
}

func TestPaginatedPagesAreIndexableAndLinked(t *testing.T) {
	s := indexedServer(t)
	resp, body := get(t, s, "/opinions/page/3/")
	canon, robots, hl := head(body)
	if resp.StatusCode != 200 || canon != Base+"/opinions/page/3/" || !strings.HasPrefix(robots, "index") || hl != 3 {
		t.Errorf("/opinions/page/3/: %d canonical %q robots %q hreflang %d", resp.StatusCode, canon, robots, hl)
	}
	for _, link := range []string{`href="/opinions/page/2/"`, `href="/opinions/page/4/"`, `href="/opinions/"`} {
		if !strings.Contains(body, link) {
			t.Errorf("page 3 does not link %s with a plain <a>", link)
		}
	}
	if strings.Contains(body, "?page=") {
		t.Error("the pager still uses ?page=")
	}
	if _, en := get(t, s, "/en/opinions/page/3/"); !strings.Contains(en, `href="/en/opinions/page/4/"`) {
		t.Error("the English pager does not stay under /en")
	}

	_, f := get(t, s, "/rulings/?decade=2010&page=2")
	if _, robots, _ := head(f); robots != "noindex, follow" {
		t.Errorf("filtered page: robots %q", robots)
	}

	for _, p := range []string{"/laws/?page=2", "/rulings/?page=2", "/gazette/?page=2"} {
		resp, deep := get(t, s, p)
		if _, robots, _ := head(deep); resp.StatusCode != 200 || robots != "noindex, follow" {
			t.Errorf("%s: %d robots %q", p, resp.StatusCode, robots)
		}
	}
	if resp, _ := get(t, s, "/laws/page/2/"); resp.StatusCode != 404 {
		t.Errorf("/laws/page/2/: %d", resp.StatusCode)
	}
}

func TestRobotsAllowsPaginationPaths(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.robots(rec, httptest.NewRequest("GET", "/robots.txt", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "Disallow: /*?") {
		t.Error("robots.txt must keep blocking query strings")
	}
	for _, p := range []string{"/opinions/page/2/", "/laws/year/2005/page/3/", "/law/244736/part/2/"} {
		for _, line := range strings.Split(body, "\n") {
			if rule, ok := strings.CutPrefix(line, "Disallow: "); ok && strings.HasPrefix(p, rule) && rule != "" {
				t.Errorf("robots.txt blocks %s with %q", p, line)
			}
		}
	}
}

func TestYearListsArePaged(t *testing.T) {
	s := indexedServer(t)
	for _, p := range []string{"/laws/year/2005/", "/laws/year/2005/page/2/", "/en/laws/year/2005/page/2/"} {
		resp, body := get(t, s, p)
		if resp.StatusCode != 200 || len(body) > 400_000 {
			t.Errorf("%s: %d, %d bytes", p, resp.StatusCode, len(body))
		}
		if n := strings.Count(body, `class="docrow"`); n > yearPageSize {
			t.Errorf("%s lists %d texts", p, n)
		}
	}

	_, first := get(t, s, "/laws/year/2005/")
	var total int
	if err := s.db.QueryRow(context.Background(), `SELECT count(*) FROM legislation WHERE issued_on >= '2005-01-01' AND issued_on < '2006-01-01'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	for n := 2; n <= (total+yearPageSize-1)/yearPageSize; n++ {
		if !strings.Contains(first, `href="/laws/year/2005/page/`+strconv.Itoa(n)+`/"`) {
			t.Errorf("year 2005 page 1 does not link page %d", n)
		}
	}
}

func TestLongTextIsInPartsUnderOneMegabyte(t *testing.T) {
	s := indexedServer(t)
	var n int
	if err := s.db.QueryRow(context.Background(), `SELECT count(*) FROM legislation WHERE id = 244736`).Scan(&n); err != nil || n == 0 {
		t.Skip("law 244736 not in this database")
	}
	resp, first := get(t, s, "/law/244736/")
	if resp.StatusCode != 200 || len(first) > 1_000_000 {
		t.Fatalf("/law/244736/: %d, %d bytes", resp.StatusCode, len(first))
	}
	parts := regexp.MustCompile(`href="(/law/244736/part/(\d+)/)"`).FindAllStringSubmatch(first, -1)
	if len(parts) < 2 {
		t.Fatalf("the first part links %d other parts", len(parts))
	}
	articles := strings.Count(first, `<article class="art`)
	seen := map[string]bool{}
	for _, m := range parts {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		resp, body := get(t, s, m[1])
		if resp.StatusCode != 200 || len(body) > 1_000_000 {
			t.Errorf("%s: %d, %d bytes", m[1], resp.StatusCode, len(body))
		}

		if canon, robots, _ := head(body); canon != Base+m[1] || !strings.HasPrefix(robots, "index") {
			t.Errorf("%s: canonical %q robots %q", m[1], canon, robots)
		}
		articles += strings.Count(body, `<article class="art`)
	}
	var want int
	s.db.QueryRow(context.Background(), `SELECT count(*) FROM legislation_article WHERE law_id = 244736`).Scan(&want)
	if articles != want {
		t.Errorf("the parts hold %d articles, the text has %d", articles, want)
	}
	if resp, _ := get(t, s, "/law/244736/part/1/"); resp.StatusCode != 301 {
		t.Errorf("part 1 is the text's own address: %d", resp.StatusCode)
	}
	if resp, _ := get(t, s, "/law/244736/part/99/"); resp.StatusCode != 404 {
		t.Errorf("a part past the last: %d", resp.StatusCode)
	}

	if resp, _ := get(t, s, "/law/244058/part/2/"); resp.StatusCode != 404 {
		t.Errorf("the Constitution has no part 2: %d", resp.StatusCode)
	}

	resp, _ = get(t, s, "/law/244736/go/?n=140&s=1")
	if resp.StatusCode != 302 || !strings.Contains(resp.Header.Get("Location"), "/article/140/") {
		t.Errorf("go to article: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}
