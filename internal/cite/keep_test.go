package cite

import "testing"

func TestKeepValidMergesEarlierValidCitations(t *testing.T) {
	a := Target{Kind: "law", Number: "90", Article: "708"}
	b := Target{Kind: "law", Number: "90", Article: "709"}
	z := Target{Kind: "law", Number: "1", Article: "2"}
	old := Citation{Model: "api:claude-opus-5-5", Changes: []Change{
		{Action: "applies", Target: a, Valid: true},
		{Action: "applies", Target: b, Valid: true},
		{Action: "cites", Target: z, Valid: false},
	}}
	c := keepValid(Citation{Model: "agy:g", Changes: []Change{{Action: "cites", Target: a, Valid: true}}}, old)
	if len(c.Changes) != 2 {
		t.Fatalf("got %d changes, want 2 (new 708 under another action, kept 709, not the bad quote): %+v", len(c.Changes), c.Changes)
	}
	if c.Changes[1].Target.Article != "709" {
		t.Errorf("kept %+v, want article 709", c.Changes[1].Target)
	}
	if c.Model != "agy:g+api:claude-opus-5-5" {
		t.Errorf("model %q", c.Model)
	}
	if same := keepValid(Citation{Model: "agy:g", Changes: []Change{{Action: "applies", Target: a, Valid: true}, {Action: "applies", Target: b, Valid: true}}}, old); same.Model != "agy:g" {
		t.Errorf("nothing kept, but model became %q", same.Model)
	}
}

func TestWantRedoKeepPrefix(t *testing.T) {
	bad := Citation{Model: "api:claude-opus-5-5", Changes: []Change{{Valid: false}}}
	good := Citation{Model: "api:claude-opus-5-5", Changes: []Change{{Valid: true}}}
	if !wantRedo(bad, "keep:invalid:opus") || wantRedo(good, "keep:invalid:opus") {
		t.Error("keep:invalid:opus should select only the answer with a bad quote")
	}
}
