package citecheck

import (
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/cite"
	"github.com/taktekhq/lebanese-legislation/internal/judge"
)

// Expected results are set by hand, one violation per condition.
func TestCheck(t *testing.T) {
	in := Input{
		New: []Article{{"100/1", "aaaa"}, {"100/2", "bbbb"}, {"100/3", "cccc"}},
		Citations: map[string]cite.Citation{
			// 100/1: no reading (fails 1)
			"100/2": {Key: "100/2", Input: "old", Prompt: cite.Prompt}, // pending (fails 2)
			"100/3": {Key: "100/3", Input: "cccc", Prompt: cite.Prompt},
		},
		Gaps: map[string][]string{"100/3": {"unresolved replace: law 999"}},
		Judged: map[string]judge.Judgement{
			"100/3/1/5": {Input: "1111"}, // judged another body (fails 3)
			"100/3/2/6": {Input: "2222"}, // matches
		},
		Recomputed: map[string]string{"100/3/1/5": "9999", "100/3/2/6": "2222"},
		LogLines:   []string{"asked 10, skipped 0, failed 0; states map[]", "read 5, skipped 0, failed 2; 3 changes"}, // fails 4
	}
	r := Check(in)
	want := []string{"no citation reading: article 100/1", "pending citation: article 100/2", "judgement 100/3/1/5", "log reports failed 2"}
	if len(r.Violations) != len(want) {
		t.Fatalf("violations = %q, want %d", r.Violations, len(want))
	}
	for i, w := range want {
		if !strings.Contains(r.Violations[i], w) {
			t.Errorf("violation %d = %q, want it to contain %q", i, r.Violations[i], w)
		}
	}
	if len(r.Gaps) != 1 || !strings.HasPrefix(r.Gaps[0], "100/3: unresolved") {
		t.Errorf("gaps = %q", r.Gaps)
	}
}

func TestCheckPassesWithGapsOnly(t *testing.T) {
	in := Input{
		New:       []Article{{"7/1", "h"}},
		Citations: map[string]cite.Citation{"7/1": {Input: "h", Prompt: cite.Prompt}},
		Gaps:      map[string][]string{"7/1": {"unresolved repeal: x"}},
		LogLines:  []string{"read 3, skipped 0, failed 0"},
	}
	r := Check(in)
	if !r.OK() || len(r.Gaps) != 1 {
		t.Errorf("want pass with 1 gap, got %+v", r)
	}
}

func TestReadIDsRejectsJunk(t *testing.T) {
	if _, err := ReadIDs("testdata/nonexistent"); err == nil {
		t.Error("missing file must error")
	}
}
