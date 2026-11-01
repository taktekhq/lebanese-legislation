package modelout

import (
	"slices"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/rulingfile"
)

// testdata/rulingfiles is a trimmed ruling-files folder: 5390 has a reading
// with a skipped page, 6419 a reading with no text, 99999 is not a ruling we
// hold, 5392's reading is cut off mid-write, 5391 has no file at the Centre.
func TestWalkReadings(t *testing.T) {
	have := map[int]bool{5390: true, 5391: true, 5392: true, 6419: true}
	var notes []string
	note := func(f string, a ...any) { notes = append(notes, strings.TrimSpace(f)) }
	got := map[int][]int{}
	counts, err := walkReadings("testdata/rulingfiles", have, note, func(rd *rulingfile.Reading, ps []rulingfile.Page) error {
		for _, p := range ps {
			got[rd.ID] = append(got[rd.ID], p.N)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !slices.Equal(got[5390], []int{1, 3}) {
		t.Errorf("pages loaded = %v, want only ruling 5390 pages 1 and 3", got)
	}
	if counts[5390] != 3 || len(counts) != 1 {
		t.Errorf("page counts = %v, want 5390: 3 (the scan's pages, skipped one included)", counts)
	}
	if len(notes) != 2 {
		t.Errorf("notes = %q, want the torn file and the foreign ruling", notes)
	}
}

func TestRulingFileIDs(t *testing.T) {
	index, err := rulingfile.Entries("testdata/rulingfiles")
	if err != nil {
		t.Fatal(err)
	}
	have := map[int]bool{5390: true, 5391: true, 5392: true, 6419: true}
	// 5391: no file; 5392: the retry after an error settled it; 99999: not held; 7: a torn line.
	if got, want := rulingFileIDs(index, have), []int{5390, 5392, 6419}; !slices.Equal(got, want) {
		t.Errorf("ids = %v, want %v", got, want)
	}
	if e := index[5392]; e.Bytes != 900 || e.SHA256 != "abcd" || e.FetchedAt.Minute() != 25 {
		t.Errorf("last row for 5392 should win, got %+v", e)
	}
}
