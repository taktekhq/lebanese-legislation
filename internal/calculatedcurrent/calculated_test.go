package calculatedcurrent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func sources(t *testing.T) []struct {
	Number string
	TextAr string `json:"text_ar"`
} {
	t.Helper()
	b, err := os.ReadFile("../site/testdata/civil-procedure-amounts.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Number string
		TextAr string `json:"text_ar"`
	}
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}
func TestReviewedRecords(t *testing.T) {
	articles, amounts := 0, 0
	for _, r := range sources(t) {
		got := Calculate(Input{LawID: 244565, Number: r.Number, Source: r.TextAr, State: "in_force"})
		if got == nil {
			continue
		}
		articles++
		amounts += strings.Count(got.TextAr, " (") - strings.Count(r.TextAr, " (")
		if got.SourceTextAr != r.TextAr || got.SourceSHA256 == "" || got.Kind != "statutory_monetary_adjustment" || got.Multiplier != 50 || got.IsPublishedWording || got.Authority.LawID != 330819 || got.Authority.Article != "1" || got.Authority.EffectiveOn != "2024-09-19" || got.Authority.URL != "https://lebaneselegislation.com/decree/330819/article/1/" {
			t.Fatalf("bad metadata: %+v", got)
		}
		if r.Number == "86" && !strings.Contains(got.TextAr, "5,000,000,000 (خمسة مليارات)") {
			t.Fatal(got.TextAr)
		}
		if Calculate(Input{LawID: 244565, Number: r.Number, Source: r.TextAr + " changed", State: "in_force"}) != nil {
			t.Fatal("stale source accepted")
		}
	}
	if articles != 34 || amounts != 56 {
		t.Fatalf("got %d records/%d occurrences", articles, amounts)
	}
}
func TestEligibility(t *testing.T) {
	var source string
	for _, r := range sources(t) {
		if r.Number == "86" {
			source = r.TextAr
		}
	}
	future := time.Now().AddDate(1, 0, 0)
	for _, in := range []Input{
		{LawID: 330819, Number: "86", Source: source, State: "in_force"},
		{LawID: 244565, Number: "86", Source: source, State: "repealed"},
		{LawID: 244565, Number: "86", Source: source, State: "annulled"},
		{LawID: 244565, Number: "86", Source: source, State: "in_force", Pending: true},
		{LawID: 244565, Number: "86", Source: source, State: "in_force", Annulled: true},
		{LawID: 244565, Number: "86", Source: source, State: "in_force", ValidFrom: &future},
	} {
		if Calculate(in) != nil {
			t.Fatalf("ineligible: %+v", in)
		}
	}
}
