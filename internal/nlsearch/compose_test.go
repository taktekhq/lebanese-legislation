package nlsearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestComposedRejectsUnbackedText(t *testing.T) {
	sources := []Passage{{ID: "s1", Text: "a"}, {ID: "s2", Text: "b"}}
	for _, raw := range []string{
		`{"paragraphs":[{"text":"x","sources":["s3"]}]}`,
		`{"paragraphs":[{"text":"x","sources":[]}]}`,
		`{"paragraphs":[{"text":"  ","sources":["s1"]}]}`,
		`{"paragraphs":[{"text":"x","sources":["s1"],"url":"https://outside.test"}]}`,
		`{"paragraphs":null}`, `{}`, `not json`,
		`{"paragraphs":[{"text":"` + strings.Repeat("x", 1501) + `","sources":["s1"]}]}`,
	} {
		if _, err := ParseComposed(raw, sources); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	p, err := ParseComposed(`{"paragraphs":[{"text":" Yes. ","sources":["s2","s2","s1"]}]}`, sources)
	if err != nil || len(p) != 1 || p[0].Text != "Yes." || strings.Join(p[0].Sources, ",") != "s2,s1" {
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
		out := `{"paragraphs":[{"text":"نعم.","sources":["s1"]}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": out}}}}}})
	}))
	defer srv.Close()
	c, err := New(Config{Provider: "gemini", Model: "m", Key: "k", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Compose(context.Background(), "هل؟", []Passage{{ID: "s1", Text: "نص"}})
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
