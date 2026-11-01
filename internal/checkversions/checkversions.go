// Package checkversions is a nightly check: every article has a current text in
// the in-force view, and Article 73 of law 244483 carries the Law 321/2023
// wording.
package checkversions

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

const (
	Art73Law    = 244483
	Art73Number = "73"
)

// Article is one legislation_article joined to its article_in_force row.
type Article struct {
	LawID               int
	Number              string
	CentreText          string
	InView              bool // the view has a row for it
	State               string
	Text                string // the in-force text; "" when NULL
	Pending             bool
	LatestVersionDate   string
	LatestAmendmentDate string
}

// Result is what the check found.
type Result struct {
	Violations []string
	Ahead      int // in-force text differs from the Centre's text
	Pending    int
	Total      int
}

// norm drops tatweel and the spacing the Centre varies, so a phrase compares by its letters.
func norm(s string) string {
	s = strings.NewReplacer("\u0640", "", "\u00ad", "", "\u200f", "", "\u200e", "").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// ReadFixture reads the phrases of the stored wording, one per line; # lines are comments.
func ReadFixture(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, norm(l))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("fixture has no phrases")
	}
	return out, nil
}

// Check applies rules 1 and 2. Article 73 is found among arts.
func Check(arts []Article, phrases []string) Result {
	res := Result{Total: len(arts)}
	found73 := false
	for _, a := range arts {
		id := fmt.Sprintf("law %d article %s", a.LawID, a.Number)
		switch {
		case !a.InView:
			res.Violations = append(res.Violations, id+": no row in article_in_force")
		case a.State == "in_force" && !a.Pending && strings.TrimSpace(a.Text) == "":
			res.Violations = append(res.Violations, id+": in force with no current text")
		}
		if a.Pending {
			res.Pending++
		}
		if a.LatestAmendmentDate != "" && a.LatestVersionDate < a.LatestAmendmentDate {
			res.Violations = append(res.Violations, fmt.Sprintf("%s: latest version date %s is before applied amendment date %s", id, a.LatestVersionDate, a.LatestAmendmentDate))
		}
		if a.InView && a.Text != "" && norm(a.Text) != norm(a.CentreText) {
			res.Ahead++
		}
		if a.LawID == Art73Law && a.Number == Art73Number {
			found73 = true
			t := norm(a.Text)
			for _, p := range phrases {
				if !strings.Contains(t, p) {
					res.Violations = append(res.Violations, fmt.Sprintf("%s: lacks Law 321/2023 wording %q", id, p))
				}
			}
		}
	}
	if !found73 {
		res.Violations = append(res.Violations, fmt.Sprintf("law %d article %s: not found", Art73Law, Art73Number))
	}
	return res
}

// PendingRose reports rule 4: pending rose by more than 1% on last night's count.
func PendingRose(now, prev int) bool {
	return float64(now) > float64(prev)*1.01
}
