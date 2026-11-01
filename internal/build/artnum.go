package build

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ArtNumCheck is what is left of the one spelling after a build: spellings
// that are not the Centre's, per table, and edge articles the named text
// does not have.
type ArtNumCheck struct {
	NonCanonical map[string]int // by table.column
	NotFound     int            // graph edges naming an article the text lacks (reported only: some texts are not in full)
	Checked      bool
}

// canonSQL is amend.IsCanonical as a Postgres regex, plus the numbers the
// Centre has of its own ("12 جديدة", "1 *").
const canonSQL = `(%[1]s IS NULL OR %[1]s ~ '^([1-9][0-9]*( مكرر( [1-9][0-9]*)?| اصدار)?|[1-9][0-9]*-[0-9]+|وحيدة)$'
	OR %[1]s IN (SELECT number FROM legislation_article))`

// checkArticleNumbers counts the non-canonical spellings left in the graph,
// the amendments and the article versions.
func checkArticleNumbers(ctx context.Context, conn *pgx.Conn) (ArtNumCheck, error) {
	c := ArtNumCheck{NonCanonical: map[string]int{}}
	for _, t := range [][2]string{
		{"graph_edge", "to_article"}, {"graph_edge", "from_article"},
		{"amendment", "article_number"}, {"amendment", "by_article"},
		{"article_version", "article_number"},
	} {
		var exists *string
		if err := conn.QueryRow(ctx, `SELECT to_regclass($1)::text`, t[0]).Scan(&exists); err != nil {
			return c, err
		}
		if exists == nil {
			continue
		}
		c.Checked = true
		var n int
		q := fmt.Sprintf("SELECT count(*) FROM %s WHERE NOT "+fmt.Sprintf(canonSQL, t[1]), t[0])
		if err := conn.QueryRow(ctx, q).Scan(&n); err != nil {
			return c, fmt.Errorf("%s.%s: %w", t[0], t[1], err)
		}
		if n > 0 {
			c.NonCanonical[t[0]+"."+t[1]] = n
		}
	}
	var ge *string
	if err := conn.QueryRow(ctx, `SELECT to_regclass('graph_edge')::text`).Scan(&ge); err != nil {
		return c, err
	}
	if ge != nil {
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM graph_edge e WHERE e.to_law_id IS NOT NULL AND e.to_article IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM legislation_article a WHERE a.law_id = e.to_law_id AND a.number = e.to_article)`).Scan(&c.NotFound); err != nil {
			return c, err
		}
	}
	return c, nil
}

// Total is every non-canonical spelling counted.
func (c ArtNumCheck) Total() int {
	n := 0
	for _, v := range c.NonCanonical {
		n += v
	}
	return n
}
