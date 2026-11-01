package build

import "testing"

func TestIssueNumberKey(t *testing.T) {
	for in, want := range map[string]string{"36": "36", "036": "36", " 36 ": "36", "36 ملحق": "36 ملحق", "0": "0", "": ""} {
		if got := issueNumberKey(in); got != want {
			t.Errorf("issueNumberKey(%q) = %q, want %q", in, got, want)
		}
	}
	if scanIssueID(2026, issueNumberKey("036"), nil) != scanIssueID(2026, "36", nil) {
		t.Error(`"036" and "36" must share an id`)
	}
}
