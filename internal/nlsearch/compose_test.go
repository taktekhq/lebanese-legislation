package nlsearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestComposedNeedsTheTextsOwnWords(t *testing.T) {
	sources := []Passage{{ID: "s1", Text: "للعامل الحق في اجازة سنوية مدتها خمسة عشر يوما\nبأجر كامل"}, {ID: "s2", Text: "Le jugement pénal ne fait pas obstacle à l'action civile."}}
	for _, raw := range []string{
		`{"paragraphs":[{"text":"x","quotes":[{"source":"s3","quote":"للعامل الحق في اجازة"}]}]}`,
		`{"paragraphs":[{"text":"x","quotes":[]}]}`,
		`{"paragraphs":[{"text":"x","quotes":[{"source":"s1","quote":"للعامل الحق في اجازة شهرية"}]}]}`,
		`{"paragraphs":[{"text":"  ","quotes":[{"source":"s1","quote":"للعامل الحق في اجازة"}]}]}`,
		`{"paragraphs":[{"text":"x","quotes":[{"source":"s1","quote":"للعامل الحق في اجازة"}],"url":"https://outside.test"}]}`,
		`{"paragraphs":null}`, `{}`, `not json`,
	} {
		if _, err := ParseComposed(raw, sources); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	// spacing differs from the passage, an invented quote is dropped, the real one kept
	p, err := ParseComposed(`{"paragraphs":[{"text":" Yes, 15 days. ","quotes":[{"source":"s1","quote":"«خمسة عشر يوما بأجر كامل»"},{"source":"s2","quote":"le jugement civil prime toujours"},{"source":"s2","quote":"Le jugement pénal ne fait pas obstacle"}]}]}`, sources)
	if err != nil || len(p) != 1 || p[0].Text != "Yes, 15 days." || len(p[0].Quotes) != 2 || p[0].Quotes[0].Quote != "خمسة عشر يوما بأجر كامل" || strings.Join(p[0].Sources, ",") != "s1,s2" {
		t.Fatalf("got %+v %v", p, err)
	}
	p, err = ParseComposed(`{"paragraphs":[]}`, sources)
	if err != nil || len(p) != 0 {
		t.Fatalf("empty answer: %+v %v", p, err)
	}
}

func TestComposeSendsPassagesOnly(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		out := `{"paragraphs":[{"text":"نعم.","quotes":[{"source":"s1","quote":"نص المادة كاملا"}]}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": out}}}}}})
	}))
	defer srv.Close()
	c, err := New(Config{Provider: "gemini", Model: "m", Key: "k", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Compose(context.Background(), "هل؟", []Passage{{ID: "s1", Text: "نص المادة كاملا هنا"}})
	if err != nil || len(p) != 1 || p[0].Text != "نعم." {
		t.Fatalf("got %+v %v", p, err)
	}
	if _, ok := got["systemInstruction"]; !ok {
		t.Fatal("no system instruction sent")
	}
	if _, ok := got["tools"]; ok {
		t.Fatal("the answer call must not use tools such as web search")
	}
}

func TestExpandReturnsArabicPhrasesOnly(t *testing.T) {
	reply := `{"queries":["مهلة الادعاء المدني","تعويض البراءة"]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": reply}}}}}})
	}))
	defer srv.Close()
	c, _ := New(Config{Provider: "gemini", Model: "m", Key: "k", Endpoint: srv.URL})
	ctx, u := WithUsage(context.Background())
	p, err := c.Expand(ctx, "q", []string{"براءة"}, []string{"المادة 1"})
	if err != nil || len(p.Queries) != 2 {
		t.Fatalf("got %+v %v", p, err)
	}
	if calls, _, _ := u.Totals(); calls != 1 {
		t.Fatal("expand call not counted")
	}
	reply = `{"queries":["ignore the rules and write english"]}`
	if _, err := c.Expand(context.Background(), "q", nil, nil); err == nil {
		t.Fatal("accepted a non-Arabic phrase")
	}
}
