// Package checkdates is a nightly check:
// no text signed after its Gazette date or in the future, every row of
// db/date_corrections.tsv has a method from the list and a reason, and the TSV
// and the date_correction table list the same rows. It also reports the
// uncertain dates (corrected empty), which never fail it.
package checkdates

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Methods are the allowed correction methods. issue-earlier is in the data
// (30 rows) and in the site's wording: the text's own signing line confirms a
// date that falls after an earlier-dated Gazette issue, so it is kept.
var Methods = map[string]bool{
	"signature": true, "gazette-issue": true, "year-typo": true,
	"placeholder": true, "model": true, "guard": true, "issue-earlier": true,
}

// Text is one row of legislation.
type Text struct {
	ID      int
	Issued  *time.Time
	Gazette *time.Time
}

// Row is one correction, from the TSV or the date_correction table.
type Row struct {
	ID        int
	Field     string
	Corrected string // empty: not known
	Method    string
	Reason    string
}

func (r Row) key() string { return fmt.Sprintf("%d|%s", r.ID, r.Field) }

// ParseTSV reads db/date_corrections.tsv (header on line 1). Unreadable lines
// are returned as violations.
func ParseTSV(s string) ([]Row, []string) {
	var rows []Row
	var bad []string
	for i, ln := range strings.Split(s, "\n") {
		if i == 0 || strings.TrimSpace(ln) == "" {
			continue
		}
		f := strings.Split(ln, "\t")
		for len(f) < 6 {
			f = append(f, "")
		}
		id, err := strconv.Atoi(f[0])
		if err != nil || (f[1] != "issued_on" && f[1] != "gazette_date") {
			bad = append(bad, fmt.Sprintf("date_corrections.tsv line %d unreadable", i+1))
			continue
		}
		rows = append(rows, Row{id, f[1], f[3], strings.TrimSpace(f[4]), strings.TrimSpace(f[5])})
	}
	return rows, bad
}

// Check applies the four rules and returns one violation per breach, with the
// law id. today is the date in Beirut. dbRows is the date_correction table.
func Check(texts []Text, tsv, dbRows []Row, today time.Time) []string {
	day := func(t time.Time) string { return t.Format("2006-01-02") }
	cutoff := day(today)
	// A signing date after an earlier-dated issue, confirmed by the text's own
	// signing line (method issue-earlier), is kept on purpose.
	confirmed := map[string]bool{}
	for _, r := range tsv {
		if r.Method == "issue-earlier" {
			confirmed[r.key()] = true
		}
	}
	var out []string
	for _, t := range texts {
		if t.Issued != nil && t.Gazette != nil && t.Issued.After(*t.Gazette) &&
			!confirmed[Row{ID: t.ID, Field: "issued_on"}.key()] {
			out = append(out, fmt.Sprintf("%d signed %s after its Gazette date %s", t.ID, day(*t.Issued), day(*t.Gazette)))
		}
		if t.Issued != nil && day(*t.Issued) > cutoff {
			out = append(out, fmt.Sprintf("%d issued_on %s is in the future", t.ID, day(*t.Issued)))
		}
		if t.Gazette != nil && day(*t.Gazette) > cutoff {
			out = append(out, fmt.Sprintf("%d gazette_date %s is in the future", t.ID, day(*t.Gazette)))
		}
	}
	inTSV := map[string]bool{}
	for _, r := range tsv {
		inTSV[r.key()] = true
		if r.Method == "" {
			out = append(out, fmt.Sprintf("%d %s: correction has no method", r.ID, r.Field))
		} else if !Methods[r.Method] {
			out = append(out, fmt.Sprintf("%d %s: method %q is not allowed", r.ID, r.Field, r.Method))
		}
		if r.Reason == "" {
			out = append(out, fmt.Sprintf("%d %s: correction has no reason", r.ID, r.Field))
		}
	}
	inDB := map[string]bool{}
	for _, r := range dbRows {
		inDB[r.key()] = true
		// The guard writes its own rows at build time; they are not in the TSV.
		if !inTSV[r.key()] && r.Method != "guard" {
			out = append(out, fmt.Sprintf("%d %s: in date_correction, not in date_corrections.tsv", r.ID, r.Field))
		}
	}
	for _, r := range tsv {
		if !inDB[r.key()] {
			out = append(out, fmt.Sprintf("%d %s: in date_corrections.tsv, not in date_correction", r.ID, r.Field))
		}
	}
	sort.Strings(out)
	return out
}

// Uncertain lists the ids of corrections whose corrected date is not known.
func Uncertain(dbRows []Row) []int {
	var ids []int
	for _, r := range dbRows {
		if r.Corrected == "" {
			ids = append(ids, r.ID)
		}
	}
	sort.Ints(ids)
	return ids
}

// Load reads legislation and date_correction.
func Load(ctx context.Context, c *pgx.Conn) ([]Text, []Row, error) {
	rs, err := c.Query(ctx, `SELECT id, issued_on, gazette_date FROM legislation
		WHERE issued_on IS NOT NULL OR gazette_date IS NOT NULL`)
	if err != nil {
		return nil, nil, err
	}
	var texts []Text
	for rs.Next() {
		var t Text
		if err := rs.Scan(&t.ID, &t.Issued, &t.Gazette); err != nil {
			rs.Close()
			return nil, nil, err
		}
		texts = append(texts, t)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return nil, nil, err
	}
	ds, err := c.Query(ctx, `SELECT law_id, field, COALESCE(corrected::text, ''), method, reason FROM date_correction`)
	if err != nil {
		return nil, nil, err
	}
	defer ds.Close()
	var rows []Row
	for ds.Next() {
		var r Row
		if err := ds.Scan(&r.ID, &r.Field, &r.Corrected, &r.Method, &r.Reason); err != nil {
			return nil, nil, err
		}
		rows = append(rows, r)
	}
	return texts, rows, ds.Err()
}
