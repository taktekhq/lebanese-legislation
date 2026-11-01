package build

import (
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/db"
)

// The shipped file parses clean: every row checks, and every article row
// has its decision's row for the text as a whole.
func TestCouncilRulingsFile(t *testing.T) {
	rows, problems := parseCouncilRulings(db.CouncilRulings)
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	// Decision 12/2026 on Law 70/2026: the text, articles 2 and 3 in part.
	got := map[string]string{}
	for _, r := range rows {
		if r.LawID == 2000200481 && r.Decision == "12/2026" {
			got[r.Number] = r.Effect
		}
	}
	for _, n := range []string{"", "2", "3"} {
		if got[n] != "annulled_in_part" {
			t.Errorf("70/2026 article %q: effect %q, want annulled_in_part", n, got[n])
		}
	}
	// Decision 5/2025 on Law 1/2025: the text declared not in force, as the
	// Gazette printed it (issue 23 of 2025).
	var found bool
	for _, r := range rows {
		if r.LawID == 333138 && r.Decision == "5/2025" {
			found = r.Number == "" && r.Effect == "not_in_force" && r.On.Format("2006-01-02") == "2025-05-20"
		}
	}
	if !found {
		t.Error("1/2025: no not_in_force row for decision 5/2025 of 2025-05-20")
	}
}

func TestCouncilRulingsProblems(t *testing.T) {
	const u = "https://www.cc.gov.lb/x.pdf"
	in := strings.Join([]string{
		"1\t3\tannulled\t9/2026\t2026-01-02\t" + u + "\ten\tar", // no row for the text
		"2\t\trepealed\t9/2026\t2026-01-02\t" + u + "\ten\tar",  // bad effect
		"3\t\tannulled\t9/2026\t2026-13-02\t" + u + "\ten\tar",  // bad date
		"4\t\tannulled\t9/2026\t2026-01-02\tsaderlex\ten\tar",   // no address
		"5\t\tannulled\t9/2026\t2026-01-02\t" + u + "\ten\t",    // empty note
		"6\t\tannulled\t9/2026\t2026-01-02\t" + u + "\ten\tar",
		"6\t1\tannulled\t9/2026\t2026-01-02\t" + u + "\ten\tar",
		"7\t\tnot_in_force\t5/2025\t2025-05-20\t" + u + "\ten\tar",
		"7\t1\tnot_in_force\t5/2025\t2025-05-20\t" + u + "\ten\tar", // whole text only
	}, "\n")
	rows, problems := parseCouncilRulings(in)
	if len(rows) != 3 || len(problems) != 6 {
		t.Fatalf("got %d rows, %d problems: %v", len(rows), len(problems), problems)
	}
}
