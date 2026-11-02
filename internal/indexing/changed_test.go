package indexing

import (
	"testing"
	"time"
)

func TestChangedOn(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		t    time.Time
		want string
	}{
		{time.Time{}, ""},
		{time.Date(1964, 3, 1, 0, 0, 0, 0, time.UTC), ""},   // the text's own date, seeded by an old build
		{time.Date(2026, 9, 24, 23, 0, 0, 0, time.UTC), ""}, // before the first fetch
		{time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), "2026-09-25"},
		{time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC), "2026-11-02"},
		{time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), ""}, // after today
	} {
		if got := ChangedOn(tc.t, now); got != tc.want {
			t.Errorf("ChangedOn(%v) = %q, want %q", tc.t, got, tc.want)
		}
	}
}
