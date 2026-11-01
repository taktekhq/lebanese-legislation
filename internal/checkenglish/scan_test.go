package checkenglish

import "testing"

func TestRepoSurfacesFlagged(t *testing.T) {
	v, err := Surface("../..")
	if err != nil || len(v) != 0 {
		t.Fatalf("surface: %v %v", v, err)
	}
}
