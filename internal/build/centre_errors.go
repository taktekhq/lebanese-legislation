package build

import (
	"strconv"
	"strings"

	"github.com/taktekhq/lebanese-legislation/db"
)

// centreErrors loads db/centre_errors.tsv: errors in the Centre's own text
// of an article, which the site and the MCP show next to it. A row whose
// article no longer exists (the Centre renumbered or fixed it) is reported
// and left out.
func (b *builder) centreErrors() error {
	cols := []string{"law_id", "number", "note_en", "note_ar"}
	r := newRows()
	for i, ln := range strings.Split(db.CentreErrors, "\n") {
		if strings.HasPrefix(ln, "#") || strings.TrimSpace(ln) == "" {
			continue
		}
		f := strings.Split(ln, "\t")
		id, err := strconv.Atoi(f[0])
		if len(f) != 4 || err != nil {
			b.rep.note("centre_errors.tsv line %d unreadable", i+1)
			continue
		}
		var ok bool
		if err := b.conn.QueryRow(b.ctx, `SELECT EXISTS (SELECT 1 FROM legislation_article WHERE law_id = $1 AND number = $2)`,
			id, f[1]).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			b.rep.note("centre error for %d article %s: no such article", id, f[1])
			continue
		}
		r.add("centre_error", cols, id, f[1], f[2], f[3])
	}
	return b.flush(r, "centre_error")
}
