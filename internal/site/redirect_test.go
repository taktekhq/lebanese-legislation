package site

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func noDBServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	old := textWords
	textWords = map[int]string{198100: "decree", 7: "text"}
	t.Cleanup(func() { textWords = old })
	return s
}

func TestOldAddressesMoveInOneHop(t *testing.T) {
	s := noDBServer(t)
	for _, c := range []struct{ from, to string }{

		{"/ar/law/198100/article/3/", "/decree/198100/article/3/"},
		{"/law/198100/article/3/", "/decree/198100/article/3/"},
		{"/text/198100/article/3/", "/decree/198100/article/3/"},
		{"/ar/law/198100/article/3", "/decree/198100/article/3/"},
		{"/law/198100/article/3", "/decree/198100/article/3/"},
		{"/ar/decree/198100/article/3/", "/decree/198100/article/3/"},
		{"/ar/text/198100/article/3", "/decree/198100/article/3/"},
		{"/ar/law/198100/article/3/rulings/", "/decree/198100/article/3/rulings/"},
		{"/ar/law/198100/article/3/?ref=x", "/decree/198100/article/3/?ref=x"},
		{"/ar//law/198100/article/3/", "/decree/198100/article/3/"},

		{"/ar/law/198100/", "/decree/198100/"},
		{"/ar/law/198100", "/decree/198100/"},
		{"/law/198100", "/decree/198100/"},
		{"/ar/law/198100/feed.xml", "/decree/198100/feed.xml"},
		{"/ar/law/198100/part/2/", "/decree/198100/part/2/"},

		{"/ar/law/7/article/1/", "/text/7/article/1/"},
		{"/decree/7/article/1", "/text/7/article/1/"},
		{"/ar/decree/7/", "/text/7/"},

		{"/en/law/198100/article/3/", "/en/decree/198100/article/3/"},
		{"/en/law/198100/article/3", "/en/decree/198100/article/3/"},

		{"/ar", "/"}, {"/ar/laws", "/laws/"}, {"/laws", "/laws/"},
	} {
		for _, method := range []string{"GET", "HEAD"} {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(method, c.from, nil))
			if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != c.to {
				t.Errorf("%s %s: %d Location %q, want one 301 to %q", method, c.from, rec.Code, rec.Header().Get("Location"), c.to)
			}
		}

		p, _, _ := strings.Cut(strings.TrimPrefix(c.to, "/en"), "?")
		if again := s.finalPath(t.Context(), p); again != p {
			t.Errorf("%s: lands on %s, which moves again to %s", c.from, c.to, again)
		}
	}
}

func TestRoutedPathsDoNotMove(t *testing.T) {
	s := noDBServer(t)
	for _, p := range []string{"/healthz", "/robots.txt", "/sitemap.xml", "/styles.css", "/ul/uploads/images/a", "/decree/198100/", "/decree/198100/article/3/", "/text/7/feed.xml"} {
		if to := s.finalPath(t.Context(), p); to != p {
			t.Errorf("%s moves to %s", p, to)
		}
	}
}

var moveSamples = map[string][]string{
	"ar-prefix":      {"/ar", "/ar/", "/ar/laws/", "/ar/law/198100/article/3/", "/ar/decree/7/feed.xml", "/ar//evil.example/"},
	"trailing-slash": {"/laws", "/law/198100", "/law/198100/article/3", "/ar/law/7/article/1", "/decree/198100/article/12-%D9%85%D9%83%D8%B1%D8%B1"},
	"word":           {"/law/198100/", "/text/198100/article/3/", "/law/7/article/1/rulings/", "/decree/7/go/", "/law/198100/feed.xml"},
	"alias":          {},
	"article":        {},
}

func TestRedirectTableHasNoChain(t *testing.T) {
	s := noDBServer(t)
	ctx := t.Context()
	for _, rule := range moveRules {
		samples, ok := moveSamples[rule.name]
		if !ok {
			t.Errorf("rule %s has no samples in moveSamples", rule.name)
			continue
		}
		for _, from := range samples {
			if _, moved := rule.move(s, ctx, from); !moved {
				t.Errorf("%s: does not move %s", rule.name, from)
			}
			to := s.finalPath(ctx, from)
			for _, again := range moveRules {
				if q, moved := again.move(s, ctx, to); moved && q != to {
					t.Errorf("%s %s -> %s: second hop by %s to %s", rule.name, from, to, again.name, q)
				}
			}

			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest("GET", from, nil))
			loc := rec.Header().Get("Location")
			if rec.Code != http.StatusMovedPermanently || loc != to {
				t.Errorf("%s %s: %d to %q, want 301 to %q", rule.name, from, rec.Code, loc, to)
			}
		}
	}
}

func TestOldAddressesOneHopWithDatabase(t *testing.T) {
	s := testServer(t)
	ctx := t.Context()
	follow := func(from, want string) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", from, nil))
		loc := rec.Header().Get("Location")
		if rec.Code != http.StatusMovedPermanently || (want != "" && loc != want) {
			t.Errorf("GET %s: %d Location %q, want 301 %q", from, rec.Code, loc, want)
			return
		}
		rec = httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", loc, nil))
		if rec.Code != 200 {
			t.Errorf("GET %s -> %s: second answer %d (Location %q), want 200", from, loc, rec.Code, rec.Header().Get("Location"))
		}
	}
	var id int
	var number string
	if err := s.db.QueryRow(ctx, `SELECT a.law_id, a.number FROM legislation_article a JOIN legislation l ON l.id = a.law_id
		WHERE l.kind LIKE 'مرسوم%' AND a.number ~ '^[0-9]+$' ORDER BY a.law_id, a.position LIMIT 1`).Scan(&id, &number); err != nil {
		t.Skipf("no decree article: %v", err)
	}
	final := artPath(id, number)
	rest := strings.TrimPrefix(final, lawPath(id))
	for _, from := range []string{
		"/ar/law/" + itoa(id) + "/" + rest, "/law/" + itoa(id) + "/" + rest, "/ar/law/" + itoa(id) + "/" + strings.TrimSuffix(rest, "/"),
		"/ar/law/" + itoa(id) + "/article/" + arabicIndic(number) + "/",
	} {
		follow(from, final)
	}
	var alias, to int
	if err := s.db.QueryRow(ctx, `SELECT a.id, a.law_id FROM legislation_alias a
		WHERE NOT EXISTS (SELECT 1 FROM legislation l WHERE l.id = a.id) ORDER BY a.id LIMIT 1`).Scan(&alias, &to); err != nil {
		t.Logf("no alias in this database: %v", err)
		return
	}
	follow("/ar/law/"+itoa(alias)+"/", lawPath(to))
	follow("/text/"+itoa(alias), lawPath(to))
}

func arabicIndic(n string) string {
	var b strings.Builder
	for _, r := range n {
		if r >= '0' && r <= '9' {
			r += '٠' - '0'
		}
		b.WriteRune(r)
	}
	return url.PathEscape(b.String())
}
