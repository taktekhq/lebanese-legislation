package site

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPossiblyAffectedNewestFirst(t *testing.T) {
	dsn := os.Getenv("LEBLEG_TEST_DSN")
	if dsn == "" {
		t.Skip("set LEBLEG_TEST_DSN to a disposable Postgres database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `
 CREATE TEMP TABLE legislation_kind(name_ar text, name_en text);
 CREATE TEMP TABLE legislation(id int, kind text, number text, issued_on date, gazette_date date, title text, status text);
 CREATE TEMP TABLE legislation_article(law_id int, number text, text_ar text);
 CREATE TEMP TABLE code(law_id int, name_en text);
 CREATE TEMP TABLE graph_edge(from_id int, from_article text, to_article text, relation text, part text, source text, to_law_id int, status text, from_type text);
 CREATE TEMP TABLE possibly_affected(law_id int, repealing_law_id int, repealing_article text, article_number text);
 INSERT INTO legislation_kind VALUES ('قانون', 'Law');
 INSERT INTO legislation VALUES
 (1, 'قانون', '1', '1991-05-29', NULL, 'Old', 'in_force'),
 (2, 'قانون', '2', '2026-08-27', NULL, 'New', 'in_force'),
 (3, 'قانون', '3', '2014-05-07', NULL, 'Middle', 'in_force'),
 (4, 'قانون', '4', NULL, NULL, 'Undated', 'in_force');
 INSERT INTO possibly_affected VALUES
 (244565, 1, '59', NULL), (244565, 2, '119', NULL),
 (244565, 3, '22', NULL), (244565, 1, '63', NULL),
 (244565, 4, '1', NULL),
 (244565, 1, '59', '11'), (244565, 2, '119', '11'),
 (244565, 3, '22', '11'), (244565, 4, '1', '11');
 INSERT INTO graph_edge(from_id, from_article, to_article, relation, source, to_law_id, status, from_type)
 SELECT id, '1', art, 'amend', 'parser', 244565, 'linked', 'legislation'
 FROM legislation CROSS JOIN (VALUES (''), ('11')) AS target(art);`)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{db: pool}
	law := &Law{LawRow: LawRow{ID: 244565}}
	article := &Article{Number: "11"}
	if err := s.attachLinks(ctx, law, map[string]*Article{"11": article}); err != nil {
		t.Fatal(err)
	}
	t.Run("decree amendments", func(t *testing.T) { assertLinksNewestFirst(t, law.Links) })
	t.Run("article amendments", func(t *testing.T) { assertLinksNewestFirst(t, article.Links) })
	for name, tc := range map[string]struct {
		got  []Affected
		want []int
	}{
		"decree":  {law.Affected, []int{2, 3, 1, 1, 4}},
		"article": {article.Affected, []int{2, 3, 1, 4}},
	} {
		t.Run(name, func(t *testing.T) {
			if len(tc.got) != len(tc.want) {
				t.Fatalf("got %d links, want %d", len(tc.got), len(tc.want))
			}
			for i, want := range tc.want {
				if tc.got[i].By.ID != want {
					t.Errorf("link %d: got law %d, want %d (newest first, undated last)", i, tc.got[i].By.ID, want)
				}
			}
		})
	}
}

func assertLinksNewestFirst(t *testing.T, links []Link) {
	t.Helper()
	for i, want := range []int{2, 3, 1, 4} {
		if len(links) != 4 {
			t.Fatalf("got %d amendment links, want 4", len(links))
		}
		if links[i].By.ID != want {
			t.Errorf("amendment %d: got law %d, want %d", i, links[i].By.ID, want)
		}
	}
}
