package cite

import "testing"

func TestWantRedo(t *testing.T) {
	good := Citation{Model: "api:claude-sonnet-5-5", Changes: []Change{{Valid: true}}}
	bad := Citation{Model: "api:claude-sonnet-5-5", Changes: []Change{{Valid: true}, {Valid: false}}}
	opusBad := Citation{Model: "api:claude-opus-5-5", Changes: []Change{{Valid: false}}}
	cases := []struct {
		c    Citation
		redo string
		want bool
	}{
		{good, "", false},
		{bad, "", false},
		{good, "sonnet", true},
		{opusBad, "sonnet", false},
		{good, "invalid:sonnet", false},
		{bad, "invalid:sonnet", true},
		{opusBad, "invalid:sonnet", false},
		{opusBad, "invalid:", true},
		{good, "invalid:", false},
	}
	for i, tc := range cases {
		if got := wantRedo(tc.c, tc.redo); got != tc.want {
			t.Errorf("case %d: wantRedo(%s, %q) = %v, want %v", i, tc.c.Model, tc.redo, got, tc.want)
		}
	}
}
