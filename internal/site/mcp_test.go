package site

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mcpDo(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mcpPost(rec, req)
	return rec
}

func TestMCPInitialize(t *testing.T) {
	s := testServer(t)
	rec := mcpDo(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not an object: %T", resp.Result)
	}
	if m["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v", m["protocolVersion"])
	}
	info, _ := m["serverInfo"].(map[string]any)
	if info["name"] != "lebaneselegislation" {
		t.Errorf("serverInfo.name = %v", info["name"])
	}
	if m["capabilities"] == nil {
		t.Errorf("missing capabilities")
	}

	rec = mcpDo(t, s, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	json.Unmarshal(rec.Body.Bytes(), &resp)
	m, _ = resp.Result.(map[string]any)
	if m["protocolVersion"] != "2025-03-26" {
		t.Errorf("did not echo a supported older protocol version: %v", m["protocolVersion"])
	}

	rec = mcpDo(t, s, `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	json.Unmarshal(rec.Body.Bytes(), &resp)
	m, _ = resp.Result.(map[string]any)
	if m["protocolVersion"] != mcpDefaultProtocol {
		t.Errorf("unknown protocol version did not fall back: %v", m["protocolVersion"])
	}
}

func TestMCPNotificationGetsNoBody(t *testing.T) {
	s := testServer(t)
	rec := mcpDo(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status %d, want 202", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("notification got a body: %q", rec.Body.String())
	}
}

func TestMCPPing(t *testing.T) {
	s := testServer(t)
	rec := mcpDo(t, s, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	var resp rpcResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error != nil {
		t.Fatalf("ping error: %+v", resp.Error)
	}
}

func TestMCPToolsList(t *testing.T) {
	s := testServer(t)
	rec := mcpDo(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not an object")
	}
	tools, ok := m["tools"].([]any)
	if !ok || len(tools) != 5 {
		t.Fatalf("expected 5 tools, got %v", m["tools"])
	}
	want := map[string]bool{"search_legislation": true, "get_law": true, "get_article": true, "get_ruling": true, "get_gazette_issue": true}
	for _, raw := range tools {
		tl := raw.(map[string]any)
		name, _ := tl["name"].(string)
		if !want[name] {
			t.Errorf("unexpected tool %q", name)
		}
		delete(want, name)
		if tl["description"] == "" || tl["description"] == nil {
			t.Errorf("%s: missing description", name)
		}
		schema, ok := tl["inputSchema"].(map[string]any)
		if !ok || schema["type"] != "object" {
			t.Errorf("%s: missing or bad inputSchema", name)
		}
	}
	if len(want) != 0 {
		t.Errorf("tools missing from tools/list: %v", want)
	}

	for _, raw := range tools {
		if raw.(map[string]any)["name"] == "get_links" {
			t.Errorf("get_links should not be listed until the graph table exists")
		}
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	s := testServer(t)
	rec := mcpDo(t, s, `{"jsonrpc":"2.0","id":1,"method":"something/else"}`)
	var resp rpcResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error == nil || resp.Error.Code != rpcMethodNotFound {
		t.Errorf("expected method-not-found error, got %+v", resp.Error)
	}
}

func TestMCPInvalidJSON(t *testing.T) {
	s := testServer(t)
	rec := mcpDo(t, s, `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Code)
	}
	var resp rpcResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error == nil || resp.Error.Code != rpcParseError {
		t.Errorf("expected parse error, got %+v", resp.Error)
	}
}

func TestMCPNotJSONRPC2(t *testing.T) {
	s := testServer(t)
	rec := mcpDo(t, s, `{"jsonrpc":"1.0","id":1,"method":"ping"}`)
	var resp rpcResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error == nil || resp.Error.Code != rpcInvalidRequest {
		t.Errorf("expected invalid-request error, got %+v", resp.Error)
	}
}

func TestMCPToolsCallUnknownTool(t *testing.T) {
	s := testServer(t)
	rec := mcpDo(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_everything","arguments":{}}}`)
	var resp rpcResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error == nil || resp.Error.Code != rpcInvalidParams {
		t.Errorf("expected invalid-params error for an unknown tool, got %+v", resp.Error)
	}
}

func toolCallResult(t *testing.T, rec *httptest.ResponseRecorder, v any) (isError bool) {
	t.Helper()
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not an object: %T", resp.Result)
	}
	if b, _ := m["isError"].(bool); b {
		isError = true
	}
	content, ok := m["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing content: %v", m)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if text == "" {
		t.Fatalf("empty content text")
	}
	if v != nil {
		if err := json.Unmarshal([]byte(text), v); err != nil {
			t.Fatalf("decode tool result %q: %v", text, err)
		}
	}
	return isError
}

func TestMCPSearchLegislation(t *testing.T) {
	s := testServer(t)
	rec := mcpDo(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_legislation","arguments":{"query":"موجبات","limit":5}}}`)
	var out struct {
		Result         apiSearchResult `json:"result"`
		NotLegalAdvice string          `json:"not_legal_advice"`
	}
	if isErr := toolCallResult(t, rec, &out); isErr {
		t.Fatalf("search returned a tool error")
	}
	if out.NotLegalAdvice == "" {
		t.Errorf("missing not_legal_advice")
	}
	if out.Result.LawCount == 0 && out.Result.ArtCount == 0 {
		t.Errorf("expected some hits for a common legal word")
	}

	rec = mcpDo(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_legislation","arguments":{"query":"x","kind":"bogus"}}}`)
	if isErr := toolCallResult(t, rec, nil); !isErr {
		t.Errorf("expected isError for an invalid kind")
	}

	rec = mcpDo(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_legislation","arguments":{"query":"قانون","limit":1000}}}`)
	if isErr := toolCallResult(t, rec, &out); isErr {
		t.Fatalf("search with an oversized limit returned a tool error")
	}
}

func TestMCPGetLawArticleRuling(t *testing.T) {
	s := testServer(t)
	var search struct {
		Result apiSearchResult `json:"result"`
	}
	rec := mcpDo(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_legislation","arguments":{"query":"موجبات","kind":"articles","limit":5}}}`)
	if isErr := toolCallResult(t, rec, &search); isErr {
		t.Fatalf("search returned a tool error")
	}
	if len(search.Result.Articles) == 0 {
		t.Skip("no article hits in this database")
	}
	hit := search.Result.Articles[0]

	var law struct {
		Result apiLawDetail `json:"result"`
	}
	callReq, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "get_law", "arguments": map[string]any{"id": hit.Law.ID}}})
	rec = mcpDo(t, s, string(callReq))
	if isErr := toolCallResult(t, rec, &law); isErr {
		t.Fatalf("get_law returned a tool error")
	}
	if law.Result.ID != hit.Law.ID || law.Result.URL == "" || law.Result.NotLegalAdvice == "" {
		t.Errorf("law detail missing fields: %+v", law.Result)
	}
	if len(law.Result.Articles) == 0 {
		t.Fatalf("law %d has no articles listed", hit.Law.ID)
	}
	art := law.Result.Articles[0]

	var article struct {
		Result apiArticleDetail `json:"result"`
	}
	callReq, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "get_article", "arguments": map[string]any{"law_id": hit.Law.ID, "number": art.Number}}})
	rec = mcpDo(t, s, string(callReq))
	if isErr := toolCallResult(t, rec, &article); isErr {
		t.Fatalf("get_article returned a tool error")
	}
	if article.Result.TextAr == "" {
		t.Errorf("article %s has empty text_ar", art.Number)
	}
	if article.Result.NotLegalAdvice == "" {
		t.Errorf("article missing not_legal_advice")
	}
	if !strings.HasPrefix(article.Result.URL, Base) {
		t.Errorf("article URL not canonical: %s", article.Result.URL)
	}

	rec = mcpDo(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_law","arguments":{"id":999999999}}}`)
	if isErr := toolCallResult(t, rec, nil); !isErr {
		t.Errorf("expected isError for an unknown law id")
	}

	rec = mcpDo(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_ruling","arguments":{"id":-1}}}`)
	if isErr := toolCallResult(t, rec, nil); !isErr {
		t.Errorf("expected isError for an invalid id")
	}
}

func TestMCPCacheReusesIdenticalCalls(t *testing.T) {
	s := testServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_legislation","arguments":{"query":"قانون","limit":3}}}`
	rec1 := mcpDo(t, s, body)
	rec2 := mcpDo(t, s, body)
	if rec1.Body.String() != rec2.Body.String() {
		t.Errorf("identical calls returned different bodies:\n%s\n%s", rec1.Body.String(), rec2.Body.String())
	}
}
