package site

// Old addresses move in one hop (redirect.go). The shapes come from the three
// moves: /ar/ to the root (21 Oct), /law/ /decree/ /text/ by kind (16 Oct),
// and article numbers (24 Oct). Decree 198100 is the text whose indexed
// /ar/law/198100/article/3/ (63 Search Console clicks) answered two 301s on
// 28 Oct: curl -L on the live site counted 2 hops to /decree/198100/article/3/,
// and 3 for the same address without its final slash.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// noDBServer is the real server without a database, with the words of two
// texts that are not laws: 198100 a decree, 7 a decision (/text/).
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
		// the indexed address and every older shape of it
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
		// the text's own page and its files
		{"/ar/law/198100/", "/decree/198100/"},
		{"/ar/law/198100", "/decree/198100/"},
		{"/law/198100", "/decree/198100/"},
		{"/ar/law/198100/feed.xml", "/decree/198100/feed.xml"},
		{"/ar/law/198100/part/2/", "/decree/198100/part/2/"},
		// the other kinds: a decision lives under /text/
		{"/ar/law/7/article/1/", "/text/7/article/1/"},
		{"/decree/7/article/1", "/text/7/article/1/"},
		{"/ar/decree/7/", "/text/7/"},
		// the English interface stays under /en/
		{"/en/law/198100/article/3/", "/en/decree/198100/article/3/"},
		{"/en/law/198100/article/3", "/en/decree/198100/article/3/"},
		// list pages
		{"/ar", "/"}, {"/ar/laws", "/laws/"}, {"/laws", "/laws/"},
	} {
		for _, method := range []string{"GET", "HEAD"} {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(method, c.from, nil))
			if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != c.to {
				t.Errorf("%s %s: %d Location %q, want one 301 to %q", method, c.from, rec.Code, rec.Header().Get("Location"), c.to)
			}
		}
		// the place it lands does not move again
		p, _, _ := strings.Cut(strings.TrimPrefix(c.to, "/en"), "?")
		if again := s.finalPath(t.Context(), p); again != p {
			t.Errorf("%s: lands on %s, which moves again to %s", c.from, c.to, again)
		}
	}
}

// Addresses a route answers as they are do not move.
func TestRoutedPathsDoNotMove(t *testing.T) {
	s := noDBServer(t)
	for _, p := range []string{"/healthz", "/robots.txt", "/sitemap.xml", "/styles.css", "/ul/uploads/images/a", "/decree/198100/", "/decree/198100/article/3/", "/text/7/feed.xml"} {
		if to := s.finalPath(t.Context(), p); to != p {
			t.Errorf("%s moves to %s", p, to)
		}
	}
}

// moveSamples are addresses each rule moves; every rule in the table has
// some, so a new rule cannot join without its chain being checked.
var moveSamples = map[string][]string{
	"ar-prefix":      {"/ar", "/ar/", "/ar/laws/", "/ar/law/198100/article/3/", "/ar/decree/7/feed.xml", "/ar//evil.example/"},
	"trailing-slash": {"/laws", "/law/198100", "/law/198100/article/3", "/ar/law/7/article/1", "/decree/198100/article/12-%D9%85%D9%83%D8%B1%D8%B1"},
	"word":           {"/law/198100/", "/text/198100/article/3/", "/law/7/article/1/rulings/", "/decree/7/go/", "/law/198100/feed.xml"},
	"alias":          {}, // reads legislation_alias: TestOldAddressesOneHopWithDatabase
	"article":        {}, // reads legislation_article: TestOldAddressesOneHopWithDatabase
}

// The redirect table has no chain: for every rule and every address it
// moves, the address finalPath gives is one no rule moves again, and the
// request that follows the redirect is not redirected a second time.
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
			// and through the server: one 301 to it, then no other redirect decision
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest("GET", from, nil))
			loc := rec.Header().Get("Location")
			if rec.Code != http.StatusMovedPermanently || loc != to {
				t.Errorf("%s %s: %d to %q, want 301 to %q", rule.name, from, rec.Code, loc, to)
			}
		}
	}
}

// With the database: the alias and article rules, and the full follow
// (the second request answers 200). It skips without a local Postgres.
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
		"/ar/law/" + itoa(id) + "/article/" + arabicIndic(number) + "/", // the article rule after the word and prefix rules
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

// arabicIndic writes the digits of n as Arabic-Indic digits, the way an
// Arabic keyboard types an article number.
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
