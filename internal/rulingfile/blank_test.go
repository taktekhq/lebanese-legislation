package rulingfile

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeJPEG writes a 400x560 grey page: white, with dark strokes where ink says.
func writeJPEG(t *testing.T, path string, ink func(x, y int) bool) {
	t.Helper()
	im := image.NewGray(image.Rect(0, 0, 400, 560))
	for y := 0; y < 560; y++ {
		for x := 0; x < 400; x++ {
			v := uint8(255)
			if ink(x, y) {
				v = 0
			}
			im.SetGray(x, y, color.Gray{v})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := jpeg.Encode(f, im, nil); err != nil {
		t.Fatal(err)
	}
}

func TestIsBlankImage(t *testing.T) {
	d := t.TempDir()
	// White with a scanner edge (a dark strip on the left margin): blank.
	edge := filepath.Join(d, "edge.jpg")
	writeJPEG(t, edge, func(x, y int) bool { return x < 12 })
	// Text lines across the sheet (every 20th row, 4 rows thick): not blank.
	text := filepath.Join(d, "text.jpg")
	writeJPEG(t, text, func(x, y int) bool { return x > 60 && x < 340 && y > 60 && y < 500 && y%20 < 4 })
	for path, want := range map[string]bool{edge: true, text: false} {
		got, err := IsBlankImage(path)
		if err != nil || got != want {
			t.Errorf("IsBlankImage(%s) = %v, %v; want %v", filepath.Base(path), got, err, want)
		}
	}
}

// A blank page is published empty and marked blank, no reader owes it, and
// the ruling is published once its other page is read.
func TestBlankPageIsReadAndNeverSent(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Dir(Path(dir, 5)), 0o755)
	tr := &Triage{ID: 5, Pages: []TriagePage{{N: 1, Conf: 85, Words: 100}, {N: 2, Blank: true}}}
	r := DefaultRouter
	for _, m := range []string{Haiku, Sonnet, Opus, Gemini, Tesseract} {
		if got := r.Missing(tr, nil, Readings{}, m); slices.Contains(got, 2) {
			t.Errorf("%s owes the blank page: %v", m, got)
		}
	}
	meta := Meta{Number: "55", Year: "2005"}
	head := "قرار رقم ٥٥ تاريخ ٢٠٠٥ وبما ان"
	for _, m := range []string{Gemini, Haiku, Tesseract} {
		if err := r.AddReadings(dir, 5, m, map[int]string{1: head}, tr, meta); err != nil {
			t.Fatal(err)
		}
	}
	rd, _ := LoadReading(dir, 5)
	var p2 *Page
	for i := range rd.Pages {
		if rd.Pages[i].N == 2 {
			p2 = &rd.Pages[i]
		}
	}
	if p2 == nil || !p2.Blank || p2.Text != "" || len(p2.Want) != 0 {
		t.Fatalf("page 2 should be published blank: %+v", rd.Pages)
	}
	if !Published(tr, rd) {
		t.Fatalf("ruling with one read page and one blank page should be published: %+v", rd)
	}
}
