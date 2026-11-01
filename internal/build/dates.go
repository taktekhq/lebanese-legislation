package build

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/db"
)

// dates fixes dates the Centre printed wrong (db/date_corrections.tsv), then
// guards the rest: no text may show a signing date after its own Gazette
// publication, or in the future. The printed value always stays, in
// date_correction, next to what legislation now holds.
func (b *builder) dates() error {
	var corrCols = []string{"law_id", "field", "printed", "corrected", "method", "reason"}
	r := newRows()
	done := map[string]bool{}
	for i, ln := range strings.Split(db.DateCorrections, "\n") {
		f := strings.Split(ln, "\t")
		if i == 0 || len(f) < 6 || strings.TrimSpace(ln) == "" {
			continue
		}
		id, err := strconv.Atoi(f[0])
		if err != nil || (f[1] != "issued_on" && f[1] != "gazette_date") {
			b.rep.note("date correction line %d unreadable", i+1)
			continue
		}
		var cur *time.Time
		err = b.conn.QueryRow(b.ctx, `SELECT `+f[1]+` FROM legislation WHERE id = $1`, id).Scan(&cur)
		if err != nil {
			b.rep.note("date correction for %d: no such text", id)
			continue
		}
		// The Centre may have fixed it since: apply only to the date we saw.
		if cur == nil || cur.Format("2006-01-02") != f[2] {
			b.rep.note("date correction for %d %s: the Centre now prints %v, not %s; left as printed", id, f[1], cur, f[2])
			continue
		}
		var corrected any
		if f[3] != "" {
			t, err := time.Parse("2006-01-02", f[3])
			if err != nil {
				b.rep.note("date correction for %d: bad date %q", id, f[3])
				continue
			}
			corrected = t
		}
		if _, err := b.conn.Exec(b.ctx, `UPDATE legislation SET `+f[1]+` = $2 WHERE id = $1`, id, corrected); err != nil {
			return err
		}
		r.add("date_correction", corrCols, id, f[1], *cur, corrected, f[4], f[5])
		done[fmt.Sprintf("%d|%s", id, f[1])] = true
		b.rep.DateFixes++
	}
	// The guard: anything still signed after its publication, or in the
	// future, loses its signing date until someone looks. Placeholder
	// Gazette dates (1 January of the signing year) lose the Gazette date.
	rows, err := b.conn.Query(b.ctx, `SELECT id, issued_on, gazette_date FROM legislation
		WHERE issued_on > now()::date OR (gazette_date IS NOT NULL AND issued_on > gazette_date)`)
	if err != nil {
		return err
	}
	type hit struct {
		id   int
		i, g *time.Time
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.i, &h.g); err != nil {
			rows.Close()
			return err
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, h := range hits {
		field, reason := "issued_on", ""
		switch {
		case h.g == nil || h.i.After(time.Now()):
			reason = "printed with a signing date in the future; signing date not known"
		case h.g.Month() == 1 && h.g.Day() == 1 && h.g.Year() == h.i.Year():
			field, reason = "gazette_date", "the Centre gives only the Gazette's year (01/01 placeholder)"
		default:
			reason = fmt.Sprintf("printed as signed after its publication on %s; signing date not known", h.g.Format("02/01/2006"))
		}
		if done[fmt.Sprintf("%d|%s", h.id, field)] {
			continue
		}
		printed := h.i
		if field == "gazette_date" {
			printed = h.g
		}
		if _, err := b.conn.Exec(b.ctx, `UPDATE legislation SET `+field+` = NULL WHERE id = $1`, h.id); err != nil {
			return err
		}
		r.add("date_correction", corrCols, h.id, field, *printed, nil, "guard", reason)
		b.rep.DateGuard++
		if b.rep.DateGuard <= 20 {
			b.rep.note("date guard: %d %s %s (%s)", h.id, field, printed.Format("2006-01-02"), reason)
		}
	}
	return b.flush(r, "date_correction")
}
