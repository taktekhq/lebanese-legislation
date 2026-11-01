package site

import "testing"

func TestRelatedSaysWhenSemanticSearchIsOff(t *testing.T) {
	var out apiSearchResult
	(&Server{}).addRelated(t.Context(), "dismissal without notice", true, &out)
	if out.Semantic != "unavailable" || out.Related != nil {
		t.Fatalf("without semantic search: %+v", out)
	}
}

func TestAmountsMatchWithOrWithoutSeparators(t *testing.T) {
	for _, q := range []string{"5,000,000,000", "5000000000", "٥٠٠٠٠٠٠٠٠٠", "5 000 000 000"} {
		if got := compactAmount(asciiDigits(q)); got != "5000000000" {
			t.Errorf("%q: %q", q, got)
		}
	}
}

// A query without a digit is not an amount: it must not pull every Civil
// Procedure article to the top (no database is touched).
func TestCalculatedMatchesNeedAnAmount(t *testing.T) {
	for _, q := range []string{"", "ab", "المحكمة", "civil procedure"} {
		if got := (&Server{}).calculatedMatches(t.Context(), q); got != nil {
			t.Errorf("%q: %v", q, got)
		}
	}
}
