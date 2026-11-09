package mcpserver

import (
	"context"
	"fmt"

	"github.com/taktekhq/lebanese-legislation/internal/memo"
)

// count is a paged tool's total, kept by s.counts (internal/memo): the same
// on every page of a walk, and with cursor pages the count(*) was most of a
// deep page's cost (about 80 ms for list_legislation).
func (s *Server) count(ctx context.Context, sql string, args ...any) (int, error) {
	k := memo.Key(sql, args...)
	if v, ok := s.counts.Get(k); ok {
		return v.(int), nil
	}
	var n int
	if err := s.db.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		return 0, err
	}
	s.counts.Put(k, n)
	return n, nil
}

// countVersion is the mark db/prod.sql puts on law_pub_count.
const countVersion = "lebleg-prod 4"

// pubCounts says whether law_pub_count (db/prod.sql) is there with the
// mark this server expects. It asks the database once; a failed check is
// not remembered and answers false, so list_legislation counts the rows
// (correct on every image, only slower).
func (s *Server) pubCounts(ctx context.Context) bool {
	if v := s.pubCount.Load(); v != nil {
		return *v
	}
	var has bool
	if err := s.db.QueryRow(ctx, `SELECT coalesce(obj_description(to_regclass('law_pub_count'), 'pg_class') = $1, false)`, countVersion).Scan(&has); err != nil {
		return false
	}
	s.pubCount.CompareAndSwap(nil, &has)
	return *s.pubCount.Load()
}

// listTotal is list_legislation's total: the texts with a publication date
// (lawDate) from lo to hi, both inclusive and either "" for open, of one of
// kinds (nil for every kind). where and params are the same filter as SQL
// on legislation l; they are counted as they are when law_pub_count is not
// there. With it, the total for a kind is its running count at the last
// day <= hi minus its running count at the last day < lo: two index reads a
// kind instead of reading every matching row (all 103,100 dated texts when
// no range is given).
func (s *Server) listTotal(ctx context.Context, lo, hi string, kinds []string, where string, params []any) (int, error) {
	if !s.pubCounts(ctx) {
		return s.count(ctx, `SELECT count(*) FROM legislation l WHERE `+where, params...)
	}
	who := `c.kind IS NULL`
	if kinds != nil {
		who = `c.kind = ks.kind`
	}
	var p []any
	upto := func(cond string) string {
		return `coalesce((SELECT c.upto FROM law_pub_count c WHERE ` + who + cond + ` ORDER BY c.day DESC LIMIT 1), 0)`
	}
	upper := ""
	if hi != "" {
		p = append(p, hi)
		upper = fmt.Sprintf(` AND c.day <= $%d::date`, len(p))
	}
	n := upto(upper)
	if lo != "" {
		p = append(p, lo)
		n += ` - ` + upto(fmt.Sprintf(` AND c.day < $%d::date`, len(p)))
	}
	if kinds == nil {
		return s.count(ctx, `SELECT (`+n+`)::bigint`, p...)
	}
	// A text has one kind, so the totals of distinct kinds add up.
	p = append(p, kinds)
	return s.count(ctx, fmt.Sprintf(`SELECT coalesce(sum(%s), 0)::bigint FROM (SELECT DISTINCT unnest($%d::text[]) AS kind) ks`, n, len(p)), p...)
}
