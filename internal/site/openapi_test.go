package site

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Inspect actual registrations, rather than a second manually maintained list.
func TestOpenAPIRoutes(t *testing.T) {
	data, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version %q", doc.OpenAPI)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "api.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	routes := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "HandleFunc" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok {
			return true
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(pattern, "GET /api/v1/") {
			return true
		}
		path := strings.TrimSuffix(strings.TrimPrefix(pattern, "GET "), "{$}")
		routes[path] = true
		if _, ok := doc.Paths[path]["get"]; !ok {
			t.Errorf("registered route %s missing GET in OpenAPI", path)
		}
		return true
	})
	if len(routes) == 0 {
		t.Fatal("no API registrations found")
	}
	for path := range doc.Paths {
		if !routes[path] {
			t.Errorf("OpenAPI path %s has no registered route", path)
		}
	}
}

func TestOpenAPIServingAndDiscovery(t *testing.T) {
	s := &Server{mux: http.NewServeMux()}
	s.apiRoutes()
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/openapi.json", nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != apiCacheControl || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("spec response: %d %v", w.Code, w.Header())
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/", nil))
	if w.Code != 200 {
		t.Fatalf("root status %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Link"), `<`+Base+`/api/v1/openapi.json>; rel="service-desc"`) {
		t.Errorf("missing service description link: %v", w.Header())
	}
	var root map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	if root["openapi"] != Base+"/api/v1/openapi.json" {
		t.Errorf("missing root discovery: %v", root)
	}
}
