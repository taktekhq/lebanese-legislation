package build

import (
	"fmt"
	"hash/crc32"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Gazette issues of the scanned texts (scans.go). The Centre's issues stop
// where its own publishing stopped, so a scanned text carries a Gazette
// number and date but no issue. This step gives each one an issue, or links it
// to the Centre's when the Centre already has that issue.
//
// Ids are stable across nightly rebuilds and never meet the Centre's
// Journal ids (below 2,000,000,000):
//
//   - a plain integer number under 1000: ScanIssueBase + year*1000 + number
//     (issue 36 of 2026 is 2002026036);
//   - anything else (a supplement "36م", "ملحق", letters): ScanIssueOddBase +
//     crc32(year|number) mod 40,000,000, moved up by one while that id is
//     taken, in order of year then number. Disjoint from the first range,
//     which ends below 2,100,000,000.
const (
	ScanIssueBase    = 2_000_000_000
	ScanIssueOddBase = 2_100_000_000
	scanIssueOddSpan = 40_000_000
)

var plainIssueNumber = regexp.MustCompile(`^[0-9]{1,3}$`)

// scanIssueID is the id of the issue of a scanned text, by the issue's year
// and number as printed. taken holds the odd-range ids already given out.
func scanIssueID(year int, number string, taken map[int]bool) int {
	if plainIssueNumber.MatchString(number) {
		n, _ := strconv.Atoi(number)
		if n > 0 {
			return ScanIssueBase + year*1000 + n
		}
	}
	id := ScanIssueOddBase + int(crc32.ChecksumIEEE([]byte(fmt.Sprintf("%d|%s", year, number))))%scanIssueOddSpan
	for taken != nil && taken[id] {
		id++
	}
	if taken != nil {
		taken[id] = true
	}
	return id
}

// issueNumberKey is an issue number as printed, trimmed, with leading zeros
// dropped from a plain number, so "036" and "36" are one issue (and one id).
func issueNumberKey(n string) string {
	n = strings.TrimSpace(n)
	if v, err := strconv.Atoi(n); err == nil && v > 0 && strings.Trim(n, "0123456789") == "" {
		return strconv.Itoa(v)
	}
	return n
}

type scanText struct {
	law    int
	number string
	issued time.Time
	date   *time.Time
	pages  *string
}

type scanIssueKey struct {
	year   int
	number string
}

func (b *builder) scanGazette() error {
	// Rerunnable on a database as it is: drop what an earlier run made.
	for _, q := range []string{
		`DELETE FROM gazette_entry WHERE law_id >= $1`,
		`DELETE FROM gazette_issue WHERE id >= $1`,
	} {
		if _, err := b.conn.Exec(b.ctx, q, ScanIDBase); err != nil {
			return err
		}
	}
	rows, err := b.conn.Query(b.ctx, `SELECT l.id, l.gazette_number, l.issued_on, l.gazette_date, l.gazette_pages
		FROM legislation l JOIN legislation_scan s ON s.law_id = l.id
		WHERE l.gazette_number IS NOT NULL AND l.gazette_number <> '' ORDER BY l.id`)
	if err != nil {
		return err
	}
	groups := map[scanIssueKey][]scanText{}
	for rows.Next() {
		var t scanText
		if err := rows.Scan(&t.law, &t.number, &t.issued, &t.date, &t.pages); err != nil {
			rows.Close()
			return err
		}
		y := t.issued.Year()
		if t.date != nil {
			y = t.date.Year()
		}
		k := scanIssueKey{y, issueNumberKey(t.number)}
		groups[k] = append(groups[k], t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// The Centre's issues by year and number.
	type centreIssue struct {
		id int
		on time.Time
	}
	centre := map[scanIssueKey][]centreIssue{}
	crows, err := b.conn.Query(b.ctx, `SELECT id, number, published_on FROM gazette_issue
		WHERE number IS NOT NULL AND published_on IS NOT NULL ORDER BY id`)
	if err != nil {
		return err
	}
	for crows.Next() {
		var c centreIssue
		var n string
		if err := crows.Scan(&c.id, &n, &c.on); err != nil {
			crows.Close()
			return err
		}
		k := scanIssueKey{c.on.Year(), issueNumberKey(n)}
		centre[k] = append(centre[k], c)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return err
	}

	keys := make([]scanIssueKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].year != keys[j].year {
			return keys[i].year < keys[j].year
		}
		return keys[i].number < keys[j].number
	})
	taken := map[int]bool{}
	for _, k := range keys {
		texts := groups[k]
		// The issue's date: the earliest one any of its texts carries.
		var on *time.Time
		for _, t := range texts {
			if t.date != nil && (on == nil || t.date.Before(*on)) {
				d := *t.date
				on = &d
			}
		}
		issue := 0
		for _, c := range centre[k] {
			if on == nil || on.Equal(c.on) {
				issue = c.id
				break
			}
		}
		if issue != 0 {
			b.rep.ScanIssuesLinked++
		} else {
			issue = scanIssueID(k.year, k.number, taken)
			if _, err := b.conn.Exec(b.ctx, `INSERT INTO gazette_issue (id, number, published_on) VALUES ($1, $2, $3)`,
				issue, k.number, on); err != nil {
				return err
			}
			b.rep.ScanIssuesNew++
		}
		for _, t := range texts {
			if _, err := b.conn.Exec(b.ctx, `INSERT INTO gazette_entry (issue_id, law_id, pages) VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING`, issue, t.law, t.pages); err != nil {
				return err
			}
			b.rep.ScanIssueTexts++
		}
	}
	return nil
}
