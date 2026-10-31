package nlsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEvidenceRejectsInventedMaterial(t *testing.T) {
	sources := []Passage{{ID: "a1", Text: "Employees receive paid annual leave. This text is internal."}}
	for _, raw := range []string{`{"source_ids":["a2"]}`, `{"source_ids":["a1"],"quote":"invented wording"}`, `{"source_ids":["a1"],"url":"https://outside.test"}`, `{"source_ids":null}`, `{}`, `{"source_ids":["a1","a1"]}`} {
		if _, err := parseEvidence(raw, sources); err == nil {
			t.Errorf("accepted unsupported output %s", raw)
		}
	}
	if _, err := ValidateQuotes([]Quote{{SourceID: "a1", Quote: "Employees receive unlimited paid leave."}}, sources); err == nil {
		t.Fatal("invented wording passed server validation")
	}

	q, err := parseEvidence(`{"source_ids":["a1"]}`, sources)
	if err != nil || len(q) != 1 {
		t.Fatalf("valid quotation rejected %v", err)
	}
	q, err = parseEvidence(`{"source_ids":[]}`, sources)
	if err != nil || len(q) != 0 {
		t.Fatalf("valid abstention rejected %v", err)
	}
}

func TestSelectUsesOnlySuppliedPassages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		var req map[string]any
		if err = json.Unmarshal(b, &req); err != nil {
			t.Error(err)
		}
		if _, ok := req["tools"]; ok {
			t.Error("external tools enabled")
		}
		if !strings.Contains(string(b), "Employees receive paid annual leave.") {
			t.Error("internal evidence missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"{\"source_ids\":[\"a1\"]}"}]}}]}`)
	}))
	defer srv.Close()
	c, _ := New(Config{Provider: "gemini", Model: "test", Key: "test", Endpoint: srv.URL})
	q, err := c.Select(context.Background(), "How much leave?", []Passage{{ID: "a1", Text: "Employees receive paid annual leave."}})
	if err != nil || len(q) != 1 {
		t.Fatalf("%v %v", q, err)
	}
}

type evidenceTransport func(*http.Request) (*http.Response, error)

func (f evidenceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSelectionModelDoesNotChangeRewritingModel(t *testing.T) {
	c, _ := New(Config{Provider: "gemini", Model: "rewrite-model", SelectModel: "evidence-model", Key: "test"})
	var paths []string
	c.http.Transport = evidenceTransport(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		text := `{"queries":[]}`
		if strings.Contains(r.URL.Path, "evidence-model") {
			text = `{"source_ids":[]}`
		}
		body, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]string{"text": text}}}}}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})
	if _, err := c.Rewrite(context.Background(), "training requirements"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Select(context.Background(), "training requirements", []Passage{{ID: "s1", Text: "An internal passage."}}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/v1beta/models/rewrite-model:generateContent" || paths[1] != "/v1beta/models/evidence-model:generateContent" {
		t.Fatalf("wrong model routing: %v", paths)
	}
}
