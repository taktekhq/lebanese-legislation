package site

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCalculatedCurrentAPI86(t *testing.T) {
	s := testServer(t)
	var out map[string]any
	getJSON(t, s, "/api/v1/law/244565/article/86/", &out)
	got, ok := out["calculated_current"].(map[string]any)
	if !ok {
		t.Fatal("article86 missing calculated_current")
	}
	if got["source_text_ar"] != out["text_ar"] || got["is_published_wording"] != false || !strings.Contains(got["text_ar"].(string), "5,000,000,000 (خمسة مليارات)") {
		t.Fatal(got)
	}
}
func TestCalculatedCurrentArticleEligibility(t *testing.T) {
	source := civilProcedureFixture(t)["86"]
	law := &Law{LawRow: LawRow{ID: 244565}}
	future := time.Now().AddDate(1, 0, 0)
	for _, a := range []*Article{
		{Number: "86", TextAr: source, From: &future},
		{Number: "86", TextAr: source, Versions: []Version{{State: "in_force", Major: 2}}},
		{Number: "86", TextAr: source, Council: []CouncilRuling{{Effect: "annulled"}}},
	} {
		if law.ArticleAmounts(a) != nil {
			t.Fatalf("ineligible article: %+v", a)
		}
	}
}
func TestCalculatedCurrentJSONContract(t *testing.T) {
	// Test the public article shape without requiring PostgreSQL.
	var out apiArticleDetail
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "calculated_current") {
		t.Fatal("nil record must be absent")
	}
	source := civilProcedureFixture(t)["86"]
	out.CalculatedCurrent = (&Law{LawRow: LawRow{ID: 244565}}).calculatedCurrent(&Article{Number: "86", TextAr: source})
	b, _ = json.Marshal(out)
	if !strings.Contains(string(b), `"is_published_wording":false`) {
		t.Fatal(string(b))
	}
}

func TestCalculatedCurrentCollectionIncludesEveryEligibleArticle(t *testing.T) {
	l := &Law{LawRow: LawRow{ID: 244565}}
	for number, text := range civilProcedureFixture(t) {
		l.List = append(l.List, &Article{Number: number, TextAr: text})
	}
	got := l.calculatedCollection()
	if len(got) != 34 {
		t.Fatalf("got %d records", len(got))
	}
	for _, a := range l.List {
		if !reflect.DeepEqual(got[a.Number], l.calculatedCurrent(a)) {
			t.Fatal(a.Number)
		}
	}
	l.List = nil
	if len(l.calculatedCollection()) != 0 {
		t.Fatal("empty corpus retained claims")
	}
}
