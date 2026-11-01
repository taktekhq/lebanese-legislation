// Package checkamounts is a nightly check:
// every row of db/amounts.tsv reaches an article, every text that changes all
// the sums in a code has a row or a review entry, and no row's edge repeals.
package checkamounts

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Pattern finds an article that changes every sum or fine of another text:
// "all amounts/fines" (جميع/كافة/كل المبالغ/الغرامات), then a code or decree,
// then a verb that changes them. The same string runs in Postgres (~) and Go.
const Pattern = `(جميع|كافة|كل)[[:space:]]+(المبالغ|الغرامات)[^.]{0,160}(قانون|مرسوم|مجموعة)[^.]{0,160}(تضاعف|تُضاعف|يضاعف|تضرب|تُضرب|ضرب|تعدل|تُعدل|ترفع|تُرفع|رفع)`

// Row is a line of db/amounts.tsv.
type Row struct {
	ByLaw int
	ByArt string
	Law   int
	Line  string
}

// Key names the changing article of a row or text, "330819/1".
func Key(law int, art string) string { return fmt.Sprintf("%d/%s", law, art) }

// Text is an article whose words match Pattern.
type Text struct {
	Law int
	Art string
}

// Data is what the check reads.
type Data struct {
	Rows   []Row
	Reach  map[string]int // editorial edges per row key that link an article
	Repeal []string       // editorial edges whose relation is not "amend", listed
	Texts  []Text
	Review map[string]bool // keys listed in the review list
	Expect map[string]int  // pinned reach per row key, from a source
}

// ParseRows reads db/amounts.tsv. Comments and the header are skipped.
func ParseRows(s string) ([]Row, []string) {
	var rows []Row
	var bad []string
	for _, line := range strings.Split(s, "\n") {
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "by_law_id") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 6 {
			bad = append(bad, "amounts.tsv: short line: "+line)
			continue
		}
		b, e1 := strconv.Atoi(f[0])
		l, e2 := strconv.Atoi(f[2])
		if e1 != nil || e2 != nil || f[1] == "" {
			bad = append(bad, "amounts.tsv: bad line: "+line)
			continue
		}
		rows = append(rows, Row{b, f[1], l, line})
	}
	return rows, bad
}

// ParseKeyed reads "by_law_id<TAB>by_article<TAB>value..." lines into key -> value
// fields (the review list, with the note in the third column; the expected
// reach, with the count there).
func ParseKeyed(s string) (map[string]string, []string) {
	m := map[string]string{}
	var bad []string
	for _, line := range strings.Split(s, "\n") {
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "by_law_id") {
			continue
		}
		f := strings.Split(line, "\t")
		id, err := strconv.Atoi(f[0])
		if err != nil || len(f) < 3 || f[1] == "" {
			bad = append(bad, "bad line: "+line)
			continue
		}
		m[Key(id, f[1])] = f[2]
	}
	return m, bad
}

// Check returns the violations and, per row, how many articles it reaches.
func Check(d Data) (viol []string, reach []string) {
	rowKeys := map[string]bool{}
	for _, r := range d.Rows {
		k := Key(r.ByLaw, r.ByArt)
		rowKeys[k] = true
		n := d.Reach[k]
		reach = append(reach, fmt.Sprintf("%s -> law %d: %d articles", k, r.Law, n))
		if n == 0 {
			viol = append(viol, fmt.Sprintf("row %s links to no article with a sum (law %d)", k, r.Law))
		}
		if want, ok := d.Expect[k]; ok && want != n {
			viol = append(viol, fmt.Sprintf("row %s reaches %d articles, expected %d", k, n, want))
		}
	}
	seen := map[string]bool{}
	for _, t := range d.Texts {
		k := Key(t.Law, t.Art)
		if seen[k] || rowKeys[k] || d.Review[k] {
			continue
		}
		seen[k] = true
		viol = append(viol, fmt.Sprintf("law %d article %s says all sums in a code change, with no row and no review entry", t.Law, t.Art))
	}
	for _, e := range d.Repeal {
		viol = append(viol, "editorial edge marks a repeal: "+e)
	}
	sort.Strings(viol)
	return viol, reach
}

// Load reads the database after the graph build.
func Load(ctx context.Context, conn *pgx.Conn, d *Data) error {
	d.Reach = map[string]int{}
	rows, err := conn.Query(ctx, `SELECT from_id, from_article, count(*) FROM graph_edge
		WHERE source = 'editorial' AND status = 'linked' AND to_article IS NOT NULL AND relation = 'amend'
		GROUP BY 1, 2`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, n int
		var art *string
		if err := rows.Scan(&id, &art, &n); err != nil {
			rows.Close()
			return err
		}
		if art != nil {
			d.Reach[Key(id, *art)] = n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = conn.Query(ctx, `SELECT from_key, coalesce(to_key, ''), relation FROM graph_edge
		WHERE source = 'editorial' AND relation <> 'amend' ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var a, b, rel string
		if err := rows.Scan(&a, &b, &rel); err != nil {
			rows.Close()
			return err
		}
		d.Repeal = append(d.Repeal, fmt.Sprintf("%s -> %s (%s)", a, b, rel))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = conn.Query(ctx, `SELECT law_id, number FROM legislation_article WHERE text_ar ~ $1 ORDER BY law_id, position`, Pattern)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var t Text
		if err := rows.Scan(&t.Law, &t.Art); err != nil {
			return err
		}
		d.Texts = append(d.Texts, t)
	}
	return rows.Err()
}
