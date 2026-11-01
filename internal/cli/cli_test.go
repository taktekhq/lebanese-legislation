package cli

import (
	"errors"
	"fmt"
	"testing"
)

type codedErr struct{ code int }

func (e codedErr) Error() string { return "wording guard: 3 articles without source" }
func (e codedErr) ExitCode() int { return e.code }

func TestExitCode(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{errors.New("plain"), 1},
		{codedErr{4}, 4},
		{fmt.Errorf("export: %w", codedErr{4}), 4}, // wrapped: still its own code
		{codedErr{0}, 1}, // an error never exits 0
	}
	for _, c := range cases {
		if got := ExitCode(c.err); got != c.want {
			t.Errorf("ExitCode(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}
