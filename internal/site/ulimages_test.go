package site

import "testing"

func TestImageURLServesULPicturesThroughTheSite(t *testing.T) {
	for in, want := range map[string]string{
		"https://legallaw.ul.edu.lb/uploads/images/22-crop.png": "/ul/uploads/images/22-crop.png",
		"http://10.135.0.250/uploads/images/23-crop.png":        "/ul/uploads/images/23-crop.png", // the Centre's internal address
		"https://example.org/a.png":                             "https://example.org/a.png",
	} {
		if got := imageURL(in); got != want {
			t.Errorf("imageURL(%q) = %q, want %q", in, got, want)
		}
	}
}
