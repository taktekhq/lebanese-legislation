package semantic

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChunksPreserveUnicodeOffsetsAndHash(t *testing.T) {
	d := Document{Kind: "article", ID: 5, Text: strings.Repeat("أ🙂é", 900)}
	cs := Chunks(d)
	if len(cs) != 4 {
		t.Fatalf("chunks=%d", len(cs))
	}
	r := []rune(d.Text)
	for i, c := range cs {
		if c.Text != string(r[c.Start:c.End]) || c.End-c.Start > 900 || c.Hash != Hash(d.Text) {
			t.Fatal("chunk lost provenance")
		}
		if i > 0 && cs[i-1].End-c.Start != 120 {
			t.Fatal("overlap")
		}
	}
	if Hash(d.Text) == Hash(d.Text+"x") {
		t.Fatal("hash unchanged")
	}
	if len(Chunks(Document{Text: "  "})) != 0 {
		t.Fatal("blank indexed")
	}
}

func TestGeminiNormalizesAndUsesTaskHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "secret" || strings.Contains(r.URL.String(), "secret") {
			t.Error("key handling")
		}
		var body struct {
			Requests []struct {
				TaskType             string `json:"taskType"`
				OutputDimensionality int    `json:"outputDimensionality"`
			} `json:"requests"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Requests) != 1 || body.Requests[0].TaskType != "RETRIEVAL_QUERY" || body.Requests[0].OutputDimensionality != 768 {
			t.Errorf("request %+v", body)
		}
		v := make([]float32, 768)
		v[0] = 3
		v[1] = 4
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": []any{map[string]any{"values": v}}})
	}))
	defer srv.Close()
	g, _ := NewGemini("secret", "")
	g.endpoint = srv.URL
	v, err := g.Embed(context.Background(), []string{"العامل"}, true)
	if err != nil || len(v) != 1 || math.Abs(float64(v[0][0])-.6) > 1e-6 {
		t.Fatalf("%v %v", v, err)
	}
}

func TestGeminiRejectsInvalidVectorsAndSafeErrors(t *testing.T) {
	for _, body := range []string{`{"embeddings":[]}`, `{"embeddings":[{"values":[1]}]}`, `{"embeddings":[{"values":` + strings.Repeat("[", 0) + `[]}]}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		g, _ := NewGemini("secret", "")
		g.endpoint = srv.URL
		if _, err := g.Embed(context.Background(), []string{"x"}, false); err == nil {
			t.Fatal("accepted invalid response")
		}
		srv.Close()
	}
	for _, bad := range []float32{0, float32(math.Inf(1)), float32(math.NaN())} {
		v := make([]float32, 768)
		v[0] = bad
		if _, err := normalize(v); err == nil {
			t.Fatal("accepted invalid vector")
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret echoed", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	g, _ := NewGemini("secret", "")
	g.endpoint = srv.URL
	_, err := g.Embed(context.Background(), []string{"x"}, false)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("unsafe error", err)
	}
}

func TestGeminiBlocksRedirects(t *testing.T) {
	reached := false
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer dest.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	g, _ := NewGemini("secret", "")
	g.endpoint = srv.URL
	if _, err := g.Embed(context.Background(), []string{"x"}, false); err == nil || reached {
		t.Fatal("followed redirect")
	}
}
