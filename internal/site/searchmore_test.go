package site

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The search page's "show more" (searchmore.go), against the corpus named by
// LEBLEG_DSN: every row a part counts is reached by following its links,
// none twice.

var (
	moreSection = regexp.MustCompile(`(?s)<h2 class="sec__title"[^>]*>([^<]*?) <span class="muted">([0-9,+]+)</span></h2>.*?<ul class="doclist">(.*?)</ul>(<p data-more-wrap[^>]*><a [^>]*href="([^"]+)")?`)
	moreRowHref = regexp.MustCompile(`<a class="docrow" href="([^"]+)"`)
)

type morePart struct {
	count string
	rows  []string
	next  string
}

func pageGet(t *testing.T, s *Server, path string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w.Code, w.Body.String()
}

// moreParts reads the parts of a results page or fragment, by heading.
func readParts(body string) map[string]morePart {
	out := map[string]morePart{}
	for _, m := range moreSection.FindAllStringSubmatch(body, -1) {
		p := morePart{count: m[2], next: html.UnescapeString(m[5])}
		for _, r := range moreRowHref.FindAllStringSubmatch(m[3], -1) {
			p.rows = append(p.rows, r[1])
		}
		out[strings.TrimSpace(m[1])] = p
	}
	return out
}

func TestSearchShowMoreWalksEveryRow(t *testing.T) {
	s := testServer(t)
	for _, c := range []struct {
		q, heading string
		least      int // rows on the first page
	}{
		{"الغرامة الاكراهية", "Articles", 15},
		{"شيك", "Articles", 15},
		{"الغرامة الاكراهية", "Rulings", 10},
		{"يوسف كنعان", "Rulings", 10},
		{"قانون العمل", "Texts", 10},
		{"technology", "Texts", 10},
	} {
		code, body := pageGet(t, s, "/en/search/?q="+url.QueryEscape(c.q))
		if code != 200 {
			t.Fatalf("%q: HTTP %d", c.q, code)
		}
		part, ok := readParts(body)[c.heading]
		if !ok {
			t.Errorf("%q: no %s on the page", c.q, c.heading)
			continue
		}
		if len(part.rows) != c.least || part.next == "" {
			t.Errorf("%q %s: first page has %d rows, next %q; want %d rows and a link", c.q, c.heading, len(part.rows), part.next, c.least)
			continue
		}
		seen := map[string]bool{}
		rows, pages := 0, 0
		for p := part; ; pages++ {
			for _, r := range p.rows {
				if seen[r] {
					t.Errorf("%q %s: %s listed twice (page %d)", c.q, c.heading, r, pages)
				}
				seen[r] = true
				rows++
			}
			if p.next == "" {
				break
			}
			if pages > 400 {
				t.Fatalf("%q %s: no end after %d pages", c.q, c.heading, pages)
			}
			code, frag := pageGet(t, s, p.next+"&frag=1")
			if code != 200 {
				t.Fatalf("%q %s: %s: HTTP %d", c.q, c.heading, p.next, code)
			}
			if strings.Contains(frag, "<html") || strings.Contains(frag, "search-meta") {
				t.Fatalf("%q %s: the fragment is a whole page or counts as a search", c.q, c.heading)
			}
			if p, ok = readParts(frag)[c.heading]; !ok || len(p.rows) == 0 {
				t.Fatalf("%q %s: a linked page has no rows (page %d)", c.q, c.heading, pages)
			}
		}
		t.Logf("%q %s: counted %s, reached %d in %d pages", c.q, c.heading, part.count, rows, pages+1)
		if !strings.HasSuffix(part.count, "+") {
			want, _ := strconv.Atoi(strings.ReplaceAll(part.count, ",", ""))
			if rows != want {
				t.Errorf("%q %s: the page counts %d, its links reach %d", c.q, c.heading, want, rows)
			}
		} else if rows < 1000 {
			t.Errorf("%q %s: counted %s, reached %d", c.q, c.heading, part.count, rows)
		}
	}
}

// Without scripts the link is a page of its own: only that part's next rows,
// a way back, and no second count of the search.
func TestSearchShowMorePage(t *testing.T) {
	s := testServer(t)
	q := url.QueryEscape("الغرامة الاكراهية")
	for _, c := range []struct{ prefix, heading, more, back string }{
		{"", "المواد", "عرض المزيد", "كل نتائج هذا البحث"},
		{"/en", "Articles", "Show more", "All results for this search"},
		{"/fr", "Articles", "Afficher plus", "Tous les résultats de cette recherche"},
	} {
		_, body := pageGet(t, s, c.prefix+"/search/?q="+q)
		first, ok := readParts(body)[c.heading]
		if !ok || first.next == "" {
			t.Fatalf("%s: no %s link on the page", c.prefix, c.heading)
		}
		if !strings.Contains(body, ">"+c.more+"</a>") || !strings.HasPrefix(first.next, c.prefix+"/search/?q=") {
			t.Errorf("%s: link text or address is wrong: %q", c.prefix, first.next)
		}
		code, page := pageGet(t, s, first.next)
		if code != 200 || !strings.Contains(page, "<html") {
			t.Fatalf("%s: %s: HTTP %d, or not a whole page", c.prefix, first.next, code)
		}
		parts := readParts(page)
		if len(parts) != 1 || len(parts[c.heading].rows) == 0 {
			t.Errorf("%s: the page lists %d parts, %d rows", c.prefix, len(parts), len(parts[c.heading].rows))
		}
		for _, r := range parts[c.heading].rows {
			if strings.Contains(strings.Join(first.rows, "\n"), r+"\n") {
				t.Errorf("%s: %s is on the first page too", c.prefix, r)
			}
		}
		if !strings.Contains(page, c.back) || strings.Contains(page, "search-meta") || strings.Contains(page, "data-ai-answer") {
			t.Errorf("%s: the page lacks the way back, or counts as a new search", c.prefix)
		}
		if !strings.Contains(page, `name="robots" content="noindex`) {
			t.Errorf("%s: the page is not noindex", c.prefix)
		}
	}
}

func TestSearchShowMoreRefusals(t *testing.T) {
	s := testServer(t)
	_, body := pageGet(t, s, "/en/search/?q="+url.QueryEscape("شيك"))
	art := readParts(body)["Articles"].next
	u, err := url.Parse(art)
	if err != nil || art == "" {
		t.Fatalf("no articles link: %q", art)
	}
	cur := u.Query().Get("cursor")
	for _, path := range []string{
		"/en/search/?q=x&part=gazette&cursor=" + cur,
		"/en/search/?q=x&part=articles&cursor=nonsense",
		"/en/search/?q=x&part=articles",
	} {
		if code, _ := pageGet(t, s, path); code != http.StatusBadRequest {
			t.Errorf("%s: HTTP %d, want 400", path, code)
		}
	}
	// A cursor of a search whose first page had no more rows of that part: no rows, no error.
	if code, _ := pageGet(t, s, "/en/search/?q="+url.QueryEscape("zzzzqqq")+"&part=articles&cursor="+cur+"&frag=1"); code != 200 {
		t.Errorf("stale cursor: HTTP %d", code)
	}
}
