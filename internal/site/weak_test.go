package site

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/archive"
	"github.com/taktekhq/lebanese-legislation/internal/build"
	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

// weakFixture is a small corpus for the weak-page rules (issue #86):
//
//   - texts 101 (popular, in the list), 102 and 103 (not popular);
//   - rulings 201 (Centre text, scan read well), 202 (Centre text, scan read
//     with low confidence), 203 (no Centre text, read well), 204 (no Centre
//     text, scan never read), 205 (Centre text, scan not read yet): every
//     ruling page is hidden, whatever its reading.
const weakFixture = `
INSERT INTO legislation_kind (name_ar, name_en) VALUES ('قانون', 'Law'), ('مرسوم', 'Decree');
INSERT INTO court (name_ar, name_en) VALUES ('محكمة التمييز', 'Court of Cassation');
INSERT INTO legislation (id, kind, number, issued_on, title, status) VALUES
  (101, 'قانون', '1', '2020-01-01', 'قانون مقروء كثيرا', 'in_force'),
  (102, 'قانون', '2', '2020-02-01', 'قانون قليل القراءة', 'in_force'),
  (103, 'مرسوم', '3', '2020-03-01', 'مرسوم قليل القراءة', 'in_force');
INSERT INTO legislation_article (law_id, position, number, text_ar)
SELECT l, n, n::text, 'تحدد هذه المادة الأحكام المتعلقة بتنظيم المكتبات العامة في البلديات وتعيين أمنائها وشروط الإعارة والمحافظة على الكتب والمخطوطات القديمة ونقلها بين الفروع ومدة الإعارة والغرامات على التأخير ' || n
FROM unnest(ARRAY[101, 102, 103]) l, generate_series(1, 2) n;
INSERT INTO ruling (id, court, number, year, decided_on, title, text_ar) VALUES
  (201, 'محكمة التمييز', '1', 2024, '2024-01-10', 'حكم رقم 1/2024', 'خلاصة الحكم الأول'),
  (202, 'محكمة التمييز', '2', 2024, '2024-01-11', 'حكم رقم 2/2024', 'خلاصة الحكم الثاني'),
  (203, 'محكمة التمييز', '3', 2024, '2024-01-12', 'حكم رقم 3/2024', NULL),
  (204, 'محكمة التمييز', '4', 2024, '2024-01-13', 'حكم رقم 4/2024', NULL),
  (205, 'محكمة التمييز', '5', 2024, '2024-01-14', 'حكم رقم 5/2024', 'خلاصة الحكم الخامس');
INSERT INTO ruling_file (ruling_id, pages, bytes, sha256, fetched_at) VALUES
  (201, 2, 1, 'x', now()), (202, 4, 1, 'x', now()), (203, 3, 1, 'x', now()), (204, 3, 1, 'x', now()), (205, 2, 1, 'x', now());
INSERT INTO ruling_page (ruling_id, n, text_ar, model, read_at, low_confidence) VALUES
  (201, 1, 'نص الصفحة الأولى', 'm', now(), false), (201, 2, 'نص الصفحة الثانية', 'm', now(), false),
  (202, 1, 'نص واضح', 'm', now(), false), (202, 2, 'نص غير واضح', 'm', now(), true),
  (202, 3, 'نص غير واضح', 'm', now(), true), (202, 4, 'نص غير واضح', 'm', now(), true),
  (203, 1, 'نص واضح', 'm', now(), false), (203, 2, 'نص واضح', 'm', now(), false);
`

var altLinkRe = regexp.MustCompile(`rel="alternate" hreflang="[^"]+" href="([^"]+)"`)

// weakDB makes a database of its own on the disposable server, loads the
// schema and the fixture, and runs the build's indexing step on it.
func weakDB(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("LEBLEG_TEST_DSN")
	if dsn == "" {
		t.Skip("set LEBLEG_TEST_DSN to a disposable Postgres database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := fmt.Sprintf("weak_pages_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Skipf("cannot create a database: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err == nil {
			c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			c.Close(context.Background())
		}
	})
	own := replaceDB(dsn, name)
	conn, err := pgx.Connect(ctx, own)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	for _, q := range []string{db.Schema, db.AmendmentSchema, db.GraphSchema, db.OfficialSchema, weakFixture} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	popular := filepath.Join(t.TempDir(), "popular.txt")
	if err := os.WriteFile(popular, []byte("# test\n101\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := build.Run(ctx, build.Options{Archive: archive.Archive{Dir: t.TempDir()}, DSN: own, Only: "indexing",
		PopularTexts: popular, Log: func(string, ...any) {}}); err != nil {
		t.Fatal(err)
	}
	return own
}

// replaceDB points a DSN (URL or key=value) at another database.
func replaceDB(dsn, name string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		head, query, _ := strings.Cut(dsn, "?")
		i := strings.LastIndex(head, "/")
		out := head[:i+1] + name
		if query != "" {
			out += "?" + query
		}
		return out
	}
	var parts []string
	for _, f := range strings.Fields(dsn) {
		if !strings.HasPrefix(f, "dbname=") {
			parts = append(parts, f)
		}
	}
	return strings.Join(append(parts, "dbname="+name), " ")
}

// Weak pages are noindex, follow, out of the sitemaps, and no hreflang names
// them; strong pages are untouched (issue #86).
func TestWeakPagesHidden(t *testing.T) {
	dsn := weakDB(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// what the build listed
	var reasons []string
	rows, err := pool.Query(ctx, `SELECT kind || ' ' || id || ' ' || reason FROM noindex_page WHERE kind = 'english' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	reasons, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(reasons, ", "), "english 102 machine_english, english 103 machine_english"; got != want {
		t.Errorf("noindex_page: %s, want %s", got, want)
	}

	s, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	const open, hidden = "index, follow, max-image-preview:large", "noindex, follow"
	pages := map[string]string{
		"/law/101/": open, "/en/law/101/": open, "/law/101/article/1/": open, "/en/law/101/article/1/": open,
		"/law/102/": open, "/en/law/102/": hidden, "/law/102/article/1/": open, "/en/law/102/article/1/": hidden,
		"/decree/103/": open, "/en/decree/103/": hidden, "/en/decree/103/article/2/": hidden,
		"/ruling/201/": hidden, "/en/ruling/201/": hidden, "/ruling/202/": hidden, "/ruling/204/": hidden,
		"/rulings/": hidden, "/en/rulings/": hidden, "/rulings/years/": hidden, "/en/rulings/years/": hidden,
		"/rulings/year/2024/": hidden, "/en/rulings/year/2024/": hidden, "/law/101/article/1/rulings/": hidden, "/en/law/101/article/1/rulings/": hidden,
		"/en/": open, "/en/laws/": open, "/en/codes/": open,
		"/topics/": hidden, "/en/topics/": hidden,
	}
	if all := topics.All(); len(all) > 0 {
		pages["/topic/"+all[0].Slug+"/"] = hidden
		pages["/en/topic/"+all[0].Slug+"/"] = hidden
	}
	robotsOf := map[string]string{}
	bodies := map[string]string{}
	for path, want := range pages {
		code, body, _ := getHTML(t, s, path)
		if code != 200 {
			t.Errorf("%s: %d", path, code)
			continue
		}
		canon, robots, _ := head(body)
		robotsOf[path], bodies[path] = robots, body
		if robots != want {
			t.Errorf("%s: robots %q, want %q", path, robots, want)
		}
		if want == hidden && strings.Contains(path, "/law/10") && canon != Base+path {
			t.Errorf("%s: a hidden English text page is its own canonical, got %q", path, canon)
		}
	}
	// hreflang: a popular text's pair names both sides; no page names a hidden one
	for _, path := range []string{"/law/101/", "/en/law/101/", "/law/101/article/1/", "/en/law/101/article/1/"} {
		if _, _, n := head(bodies[path]); n != 3 {
			t.Errorf("%s: %d hreflang links, want 3", path, n)
		}
	}
	for path, body := range bodies {
		for _, m := range altLinkRe.FindAllStringSubmatch(body, -1) {
			target := strings.TrimPrefix(m[1], Base)
			r, ok := robotsOf[target]
			if !ok {
				_, b, _ := getHTML(t, s, target)
				_, r, _ = head(b)
			}
			if r != open {
				t.Errorf("%s names hreflang %s, which is %q", path, target, r)
			}
		}
	}
	for _, path := range []string{"/law/102/", "/law/102/article/1/", "/decree/103/"} {
		if _, _, n := head(bodies[path]); n != 0 {
			t.Errorf("%s: %d hreflang links to a hidden English twin", path, n)
		}
	}

	// the sitemaps
	listed := map[string]bool{}
	_, body := get(t, s, "/sitemap.xml")
	for _, sm := range parseSitemap(t, body).Sitemaps {
		resp, b := get(t, s, strings.TrimPrefix(sm.Loc, Base))
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", sm.Loc, resp.StatusCode)
		}
		for _, u := range parseSitemap(t, b).URLs {
			listed[strings.TrimPrefix(u.Loc, Base)] = true
		}
	}
	for path, want := range pages {
		if strings.Contains(path, "/topic") || path == "/en/law/102/" || path == "/en/decree/103/" || strings.Contains(path, "/ruling") || strings.Contains(path, "/law/10") && !strings.Contains(path, "article") {
			if got := listed[path]; got != (want == open) {
				t.Errorf("%s: in the sitemaps %v, robots %q", path, got, want)
			}
		}
	}
	for path := range listed {
		if p := strings.TrimPrefix(path, "/en"); strings.HasPrefix(p, "/topic") || strings.HasPrefix(p, "/ruling") {
			t.Errorf("a sitemap lists %s", path)
		}
	}
	if _, b := get(t, s, "/sitemap.xml"); strings.Contains(b, "rulings-") {
		t.Error("the sitemap index names a rulings file")
	}
	if !listed["/law/102/article/1/"] || !listed["/law/101/article/1/"] {
		t.Error("the Arabic articles left the sitemaps")
	}
	for _, path := range []string{"/sitemaps/laws-1.xml"} {
		if _, b := get(t, s, path); strings.Contains(b, "/en/law/102/") || !strings.Contains(b, "/en/law/101/") {
			t.Errorf("%s: %s", path, b)
		}
	}
}
