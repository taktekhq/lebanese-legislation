package build

// LEBLEG_TEST_PG (a throwaway Postgres, as deploy/checks-dbtest.sh starts) runs
// the page_state step on a few texts in a database of its own.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// A page's date is when the page last changed here, not the text's own date.
// Law 7815 is a decree of 8 Mar 1964: ar-laws-1.xml gave its page 1964-03-08
// on 28 Oct 2026. The scraper first fetched it on 26 Sep 2026
// (source_page.first_seen, live database 28 Oct 2026), so that is the
// earliest its page can have changed.
func TestPageStateIsWhenThePageChanged(t *testing.T) {
	admin := os.Getenv("LEBLEG_TEST_PG")
	if admin == "" {
		t.Skip("LEBLEG_TEST_PG: a throwaway Postgres (deploy/checks-dbtest.sh)")
	}
	ctx := context.Background()
	ac, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ac.Close(ctx) }()
	if _, err := ac.Exec(ctx, `DROP DATABASE IF EXISTS pagestate_test`); err != nil {
		t.Fatal(err)
	}
	if _, err := ac.Exec(ctx, `CREATE DATABASE pagestate_test`); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(admin)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = "pagestate_test"
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	b := &builder{ctx: ctx, conn: conn}
	if err := b.schema(); err != nil {
		t.Fatal(err)
	}
	day := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO legislation_kind (name_ar) VALUES ('مرسوم');
		INSERT INTO legislation (id, kind, title, status, issued_on, gazette_date) VALUES
		  (7815, 'مرسوم', 'decree of 1964', 'in_force', '1964-03-08', '1964-03-12'),
		  (7816, 'مرسوم', 'amended after it was fetched', 'in_force', '1933-10-29', NULL),
		  (7817, 'مرسوم', 'changed at the Centre', 'in_force', '1921-11-30', NULL),
		  (7818, 'مرسوم', 'no source page', 'in_force', '1934-07-08', NULL),
		  (7819, 'مرسوم', 'the amending text', 'in_force', '2026-10-01', NULL);
		INSERT INTO source_page (doc_type, centre_id, url, archive_path, sha256, first_seen, last_changed) VALUES
		  ('law', 7815, 'u', 'p', 's', '2026-09-26 08:00Z', '2026-09-26 08:00Z'),
		  ('lawtext', 7815, 'u', 'p', 's', '2026-09-30 08:00Z', '2026-09-30 08:00Z'),
		  ('law', 7816, 'u', 'p', 's', '2026-09-26 08:00Z', '2026-09-26 08:00Z'),
		  ('law', 7817, 'u', 'p', 's', '2026-09-25 08:00Z', '2026-10-15 08:00Z'),
		  ('law', 7819, 'u', 'p', 's', '2026-10-02 08:00Z', '2026-10-02 08:00Z');
		INSERT INTO amendment (id, by_law_id, by_article, seq, action, law_id, effective_on, status, method) VALUES
		  (1, 7819, '1', 1, 'replace', 7816, '2026-10-10', 'applied', 'pattern'),
		  (2, 7819, '2', 1, 'replace', 7815, '1980-01-01', 'applied', 'pattern')`); err != nil {
		t.Fatal(err)
	}
	dates := func() map[int]time.Time {
		t.Helper()
		if err := b.pageState(); err != nil {
			t.Fatal(err)
		}
		rows, err := conn.Query(ctx, `SELECT id, changed_at FROM page_state WHERE kind = 'law'`)
		if err != nil {
			t.Fatal(err)
		}
		m := map[int]time.Time{}
		for rows.Next() {
			var id int
			var at time.Time
			if err := rows.Scan(&id, &at); err != nil {
				t.Fatal(err)
			}
			m[id] = at.UTC()
		}
		return m
	}
	want := map[int]string{
		7815: "2026-09-26", // first fetched; not 1964-03-08 (issued), 1964-03-12 (gazette) nor 1980-01-01 (an amendment before the page existed)
		7816: "2026-10-10", // an amendment came into force after the page existed
		7817: "2026-10-15", // the Centre changed it after the first fetch
		7818: "2026-09-25", // no source of its own: the first fetch of any
		7819: "2026-10-02",
	}
	check := func(run string) {
		t.Helper()
		got := dates()
		for id, w := range want {
			if g := got[id].Format("2006-01-02"); g != w {
				t.Errorf("%s: law %d dated %s, want %s", run, id, g, w)
			}
		}
	}
	check("first build")
	// a database from an earlier build that dated pages by the text: 7815 at
	// its own date, content unchanged; 7816 at a real later change, kept
	if _, err := conn.Exec(ctx, `UPDATE page_state SET changed_at = $1 WHERE kind = 'law' AND id = 7815`, day("1964-03-08")); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `UPDATE page_state SET changed_at = $1 WHERE kind = 'law' AND id = 7816`, day("2026-10-20")); err != nil {
		t.Fatal(err)
	}
	want[7816] = "2026-10-20"
	check("rebuild")
}
