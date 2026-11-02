package site

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type openapiDoc struct {
	OpenAPI string         `json:"openapi"`
	Info    map[string]any `json:"info"`
	Paths   map[string]map[string]struct {
		OperationID string           `json:"operationId"`
		Parameters  []map[string]any `json:"parameters"`
		Responses   map[string]any   `json:"responses"`
	} `json:"paths"`
	Components map[string]map[string]any `json:"components"`
}

func loadOpenAPI(t *testing.T) (openapiDoc, any) {
	t.Helper()
	var doc openapiDoc
	var raw any
	if err := json.Unmarshal(openapiJSON, &doc); err != nil {
		t.Fatalf("openapi.json does not parse: %v", err)
	}
	if err := json.Unmarshal(openapiJSON, &raw); err != nil {
		t.Fatal(err)
	}
	return doc, raw
}

// resolve follows a local $ref ("#/components/parameters/LawID").
func resolveRef(t *testing.T, raw any, ref string) map[string]any {
	t.Helper()
	cur := raw
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("$ref %s does not resolve", ref)
		}
		if cur, ok = m[part]; !ok {
			t.Fatalf("$ref %s does not resolve", ref)
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		t.Fatalf("$ref %s is not an object", ref)
	}
	return m
}

// /openapi.json is OpenAPI 3.1, names every route of /api/v1/ the server
// serves and nothing else, declares each path parameter, and every $ref in
// it resolves.
func TestOpenAPIDescribesEveryRoute(t *testing.T) {
	doc, raw := loadOpenAPI(t)
	if doc.OpenAPI != "3.1.0" || doc.Info["title"] == nil || doc.Info["version"] == nil {
		t.Fatalf("not an OpenAPI 3.1 document: openapi %q, info %v", doc.OpenAPI, doc.Info)
	}
	s := noDBServer(t)
	var served []string
	for _, e := range s.apiEndpoints() {
		served = append(served, e.path)
		op, ok := doc.Paths[e.path]["get"]
		if !ok {
			t.Errorf("openapi.json does not describe GET %s", e.path)
			continue
		}
		if op.OperationID == "" || op.Responses["200"] == nil {
			t.Errorf("GET %s: no operationId or 200 response", e.path)
		}
		declared := map[string]bool{}
		for _, p := range op.Parameters {
			if ref, ok := p["$ref"].(string); ok {
				p = resolveRef(t, raw, ref)
			}
			if p["in"] == "path" {
				declared[p["name"].(string)] = true
			}
		}
		for _, m := range regexp.MustCompile(`\{(\w+)\}`).FindAllStringSubmatch(e.path, -1) {
			if !declared[m[1]] {
				t.Errorf("GET %s: path parameter %s is not declared", e.path, m[1])
			}
		}
	}
	for p := range doc.Paths {
		if !slices.Contains(served, p) {
			t.Errorf("openapi.json describes %s, which the server does not serve", p)
		}
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				if k == "$ref" {
					resolveRef(t, raw, c.(string))
				}
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(raw)
}

// Each record's schema has updated_at, as the responses do.
func TestOpenAPIHasUpdatedAt(t *testing.T) {
	doc, _ := loadOpenAPI(t)
	schemas := doc.Components["schemas"]
	for _, name := range []string{"LawSummary", "ArticleDetail", "RulingDetail", "GazetteDetail", "RulingHit", "GazetteHit"} {
		props, _ := schemas[name].(map[string]any)["properties"].(map[string]any)
		if props["updated_at"] == nil {
			t.Errorf("schema %s has no updated_at", name)
		}
	}
}

// The server, live or static, answers /openapi.json with the document.
func TestOpenAPIServed(t *testing.T) {
	s := noDBServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/openapi.json", nil))
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || rec.Body.String() != string(openapiJSON) {
		t.Fatalf("GET /openapi.json: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
