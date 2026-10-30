package site

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRulingArticleLinksFixture(t *testing.T) {
	dsn := os.Getenv("LEBLEG_TEST_DSN")
	if dsn == "" {
		t.Skip("set LEBLEG_TEST_DSN to a disposable Postgres database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE legislation_kind(name_ar text primary key, centre_id int, name_en text);
		CREATE TEMP TABLE court(name_ar text primary key, centre_id int, name_en text);
		CREATE TEMP TABLE code(slug text, law_id int);
		CREATE TEMP TABLE gazette_entry(law_id int, issue_id int);
		CREATE TEMP TABLE legislation(id int primary key, kind text, number text, issued_on date, title text, status text, articles int, gazette_number text, gazette_date date, gazette_pages text, preamble text, explanatory_note text, closing text);
		CREATE TEMP TABLE source_page(doc_type text, centre_id int, url text);
		CREATE TEMP TABLE legislation_section(id int, law_id int, position int, depth int, heading text, note text);
		CREATE TEMP TABLE legislation_article(law_id int, position int, number text, note text, text_ar text, text_fr text, images text[], in_force_from date, section_id int);
		CREATE TEMP TABLE centre_error(law_id int, number text, note_en text, note_ar text);
		CREATE TEMP TABLE legislation_title_en(law_id int, title_en text);
		CREATE TEMP TABLE date_correction(law_id int, field text, printed date, corrected date, method text);
		CREATE TEMP TABLE legislation_scan(law_id int, source text, source_url text, pages int, agreement float, flagged int);
		CREATE TEMP TABLE amendment(id int, action text, article_number text, part text, text_ar text, effective_on date, status text, by_article text, law_id int, by_law_id int, seq int);
		CREATE TEMP TABLE article_version(law_id int, article_number text, major int, minor int, valid_from date, valid_to date, state text, text_ar text, text_source text, amendment_id int, note text);
		CREATE TEMP TABLE possibly_affected(law_id int, repealing_law_id int, repealing_article text, article_number text);
		CREATE TEMP TABLE ruling(id int primary key, court text, number text, year smallint, decided_on date, title text, president text, members text[], text_ar text);
		CREATE TEMP TABLE graph_edge(id int, from_key text, to_key text, relation text, source text, status text, from_type text, from_id int, from_article text, to_law_id int, to_article text, to_cite text, part text, how text, quote text, model text);
		CREATE TEMP TABLE reference(from_type text, from_id int, relation text, law_id int, article_number text, label text);
		CREATE TEMP TABLE ruling_file(ruling_id int, bytes int, pages int);
		CREATE TEMP TABLE ruling_page(ruling_id int, n int, text_ar text, model text, low_confidence boolean);`); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "rulinglinks", "fixture.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(fixture)); err != nil {
		t.Fatal(err)
	}
	conn.Release()
	s, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, prefix string }{
		{"/ruling/7002/", "/"},
		{"/en/ruling/7002/", "/en/"},
	} {
		code, body, _ := getHTML(t, s, tc.path)
		if code != 200 {
			t.Errorf("%s: status=%d", tc.path, code)
			continue
		}
		for n := 1; n <= 35; n++ {
			href := tc.prefix + "law/7001/article/" + itoa(n) + "/"
			if !strings.Contains(body, `href="`+href+`"`) {
				t.Errorf("%s: missing ruling link %q", tc.path, href)
			}
		}
		if strings.Contains(body, `href="`+tc.prefix+`law/7001/"`) && !strings.Contains(body, `href="`+tc.prefix+`law/7001/article/`) {
			t.Errorf("%s: whole-text-only graph edge should not be rendered as an article link", tc.path)
		}
	}
	for _, tc := range []struct{ path, prefix string }{
		{"/law/7001/article/28/", "/"},
		{"/en/law/7001/article/28/", "/en/"},
	} {
		code, body, _ := getHTML(t, s, tc.path)
		if code != 200 || !strings.Contains(body, `href="`+tc.prefix+`ruling/7002/"`) || !strings.Contains(body, `href="`+tc.prefix+`ruling/7003/"`) {
			t.Errorf("%s: status=%d missing ruling links", tc.path, code)
		}

		if strings.Count(body, `href="`+tc.prefix+`ruling/7002/"`) != 1 || strings.Count(body, `href="`+tc.prefix+`ruling/7003/"`) != 1 {
			t.Errorf("%s: duplicate ruling links", tc.path)
		}
		if strings.Index(body, tc.prefix+"ruling/7002/") > strings.Index(body, tc.prefix+"ruling/7003/") {
			t.Errorf("%s: rulings not newest first", tc.path)
		}
	}
	code, body, _ := getHTML(t, s, "/law/7001/article/28/rulings/")
	if code != 200 || !strings.Contains(body, "/ruling/7002/") || !strings.Contains(body, "/ruling/7003/") {
		t.Errorf("full list: status=%d body missing ruling link", code)
	}
	code, enBody, _ := getHTML(t, s, "/en/law/7001/article/28/rulings/")
	if code != 200 || !strings.Contains(enBody, "/en/ruling/7002/") || !strings.Contains(enBody, "/en/ruling/7003/") {
		t.Errorf("English full list: status=%d body missing ruling link", code)
	}
	if strings.Index(body, "/ruling/7002/") > strings.Index(body, "/ruling/7003/") {
		t.Errorf("full list: rulings not newest first")
	}
	if strings.Count(body, `href="/ruling/7002/"`) != 1 || strings.Count(body, `href="/ruling/7003/"`) != 1 {
		t.Errorf("full list has duplicate ruling links")
	}
}
