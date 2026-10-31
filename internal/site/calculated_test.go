package site

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func calculatedFixture(source string) (string, map[string]any) {
	sum := sha256.Sum256([]byte(source))
	digest := hex.EncodeToString(sum[:])
	return digest, map[string]any{
		"kind": "statutory_monetary_adjustment", "is_published_wording": false, "multiplier": 50,
		"authority":      map[string]any{"law_id": 330819, "article": "1", "url": "https://lebaneselegislation.com/decree/330819/article/1/", "effective_on": "2024-09-19"},
		"source_text_ar": source, "source_sha256": digest, "text_ar": "خمسة مليارات ليرة",
	}
}

func TestValidCalculatedAcceptsOnlyTheExactSource(t *testing.T) {
	now := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	source := "مئة مليون ليرة"
	digest, obj := calculatedFixture(source)
	raw, _ := json.Marshal(obj)
	if !validCalculated(digest, raw, source, now) {
		t.Fatal("a matching export must be accepted")
	}
	if validCalculated(digest, raw, source+" ", now) {
		t.Fatal("a different shown text must be refused")
	}
	if validCalculated(digest, raw, source, time.Date(2024, 9, 18, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("before the decree took effect must be refused")
	}
	for name, change := range map[string]func(map[string]any){
		"multiplier":  func(o map[string]any) { o["multiplier"] = 100 },
		"published":   func(o map[string]any) { o["is_published_wording"] = true },
		"authority":   func(o map[string]any) { o["authority"].(map[string]any)["law_id"] = 1 },
		"kind":        func(o map[string]any) { o["kind"] = "other" },
		"hash":        func(o map[string]any) { o["source_sha256"] = "0" },
		"same text":   func(o map[string]any) { o["text_ar"] = source },
		"empty text":  func(o map[string]any) { o["text_ar"] = " " },
		"no multiple": func(o map[string]any) { delete(o, "multiplier") },
	} {
		_, o := calculatedFixture(source)
		change(o)
		raw, _ := json.Marshal(o)
		if validCalculated(digest, raw, source, now) {
			t.Errorf("%s: a changed export must be refused", name)
		}
	}
}

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
