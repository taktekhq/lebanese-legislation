package mcpserver

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// The MCP JSON-RPC 2.0 layer: initialize, notifications/initialized, ping,
// tools/list, tools/call.

const defaultProtocol = "2025-06-18"

var supportedProtocols = setOf("2025-06-18", "2025-03-26")

const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
)

// toolsJSON is the tools/list answer's tools, byte for byte as written
// (names, titles, descriptions, input schemas and annotations).
//
//go:embed tools.json
var toolsJSON []byte

var (
	toolsCompact = func() json.RawMessage {
		var b bytes.Buffer
		if err := json.Compact(&b, toolsJSON); err != nil {
			panic("mcpserver: tools.json: " + err.Error())
		}
		return unescapeLineSeparators(b.Bytes())
	}()
	toolNames = func() []string {
		var defs []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(toolsJSON, &defs); err != nil {
			panic("mcpserver: tools.json: " + err.Error())
		}
		names := make([]string, len(defs))
		for i, d := range defs {
			names[i] = d.Name
		}
		return names
	}()
)

// Tools is the tools/list answer's tools (name, title, description, input
// schema), for the server card the website publishes.
func Tools() json.RawMessage { return toolsCompact }

const calculatedCurrentGuidance = "When calculated_current is present, use its text_ar for current monetary amounts and cite its authority.url. " +
	"Identify this as calculated under the authority, never as verbatim published wording. " +
	"The article text_ar and source fields remain published wording. "

const instructions = "Lebanese Legislation: every law, article, court ruling and Official Gazette issue published by the " +
	"Lebanese University's Centre for Research and Studies in Legal Informatics. Published source text is copied " +
	"verbatim from that database; nothing here is written or rewritten by a model. " + calculatedCurrentGuidance + pagingGuidance + NotLegalAdvice

// pagingGuidance tells agents how pages work since 2.0.0 (internal/mcpserver/paging.go).
const pagingGuidance = "Long lists come a page at a time: for the next page pass the answer's next_cursor as cursor, " +
	"unchanged, with the same other arguments; next_cursor is absent on the last page. offset was removed in version 2.0.0. "

func isKnownTool(name string) bool {
	for _, n := range toolNames {
		if n == name {
			return true
		}
	}
	return false
}

// rpcRequest is one parsed JSON-RPC request. id is nil when the request had
// no "id" member at all (a notification); an explicit null is kept as the
// raw "null", since that is still a request.
type rpcRequest struct {
	isObject bool
	jsonrpc  json.RawMessage
	id       json.RawMessage
	method   json.RawMessage
	params   json.RawMessage
}

func parseRequest(body []byte) (rpcRequest, error) {
	var req rpcRequest
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		// an empty body reads as {}
		return rpcRequest{isObject: true}, nil
	}
	if !json.Valid(body) {
		return req, errors.New("invalid JSON")
	}
	if body[0] != '{' {
		return req, nil // an array, a string, a number: not a request
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return req, err
	}
	req.isObject = true
	req.jsonrpc = m["jsonrpc"]
	req.method = m["method"]
	req.params = m["params"]
	if id, has := m["id"]; has {
		req.id = id
	}
	return req, nil
}

// valid is the Worker's `req.jsonrpc === "2.0" && req.method`.
func (r rpcRequest) valid() bool {
	if !r.isObject || string(r.jsonrpc) != `"2.0"` || len(r.method) == 0 {
		return false
	}
	var m any
	if err := decodeJSON(r.method, &m); err != nil {
		return false
	}
	return truthy(m)
}

func (r rpcRequest) methodName() string {
	var m any
	if err := decodeJSON(r.method, &m); err != nil {
		return ""
	}
	return jsString(m)
}

func decodeJSON(raw json.RawMessage, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(v)
}

func rpcError(id json.RawMessage, code int, message string) obj {
	o := obj{{"jsonrpc", "2.0"}}
	if id != nil {
		o.set("id", id)
	}
	o.set("error", obj{{"code", code}, {"message", message}})
	return o
}

func rpcResult(id json.RawMessage, result any) obj {
	o := obj{{"jsonrpc", "2.0"}}
	if id != nil {
		o.set("id", id)
	}
	o.set("result", result)
	return o
}

func initializeResult(params map[string]any) obj {
	version := defaultProtocol
	if pv, isStr := params["protocolVersion"].(string); isStr && supportedProtocols[pv] {
		version = pv
	}
	return obj{
		{"protocolVersion", version},
		{"serverInfo", obj{{"name", ServerName}, {"version", ServerVersion}}},
		{"capabilities", obj{{"tools", obj{}}}},
		{"instructions", instructions},
	}
}

func objectParams(raw json.RawMessage) map[string]any {
	var p map[string]any
	if len(raw) == 0 || decodeJSON(raw, &p) != nil || p == nil {
		return map[string]any{}
	}
	return p
}

func optString(v any) any {
	if s, isStr := v.(string); isStr {
		return s
	}
	return nil
}

// handleRPC answers one JSON-RPC request.
func (s *Server) handleRPC(r *http.Request, req rpcRequest) obj {
	switch req.methodName() {
	case "initialize":
		p := objectParams(req.params)
		return rpcResult(req.id, initializeResult(p))
	case "ping":
		return rpcResult(req.id, obj{})
	case "tools/list":
		return rpcResult(req.id, obj{{"tools", toolsCompact}})
	case "tools/call":
		return s.handleToolsCall(r.Context(), req)
	}
	return rpcError(req.id, rpcMethodNotFound, "unknown method "+req.methodName())
}

func (s *Server) handleToolsCall(ctx context.Context, req rpcRequest) obj {
	p := objectParams(req.params)
	if !truthy(p["name"]) {
		return rpcError(req.id, rpcInvalidParams, "tools/call needs a tool name")
	}
	name := jsString(p["name"])
	a, isObj := p["arguments"].(map[string]any)
	if !isObj {
		a = map[string]any{}
	}
	if !isKnownTool(name) {
		return rpcError(req.id, rpcInvalidParams, "unknown tool "+name)
	}
	argsJSON := mustMarshal(a)
	key := name + "\x00" + argsJSON
	if hit, found := s.results.get(key); found {
		return rpcResult(req.id, toolContent(hit.text, false))
	}

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	res, err := s.callTool(ctx, name, args(a))
	if err == nil && ctx.Err() != nil {
		err = errTimeout
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = errTimeout
		}
		s.logf("mcp tools/call %s: %v", name, err)
		return rpcError(req.id, rpcInternalError, "internal error")
	}
	text := mustMarshal(res.body)
	count := resultCount(res.body)
	if !res.isError {
		s.results.put(key, text, count)
	}
	return rpcResult(req.id, toolContent(text, res.isError))
}

func toolContent(text string, isError bool) obj {
	return obj{{"content", []any{obj{{"type", "text"}, {"text", text}}}}, {"isError", isError}}
}

// resultCount adds up a tool result's *_count fields (searches), or counts
// it when it is a list; nil when it has neither.
func resultCount(body any) any {
	if o, isObj := body.(obj); isObj {
		if r, has := o.get("result"); has {
			body = r
		}
	}
	switch b := body.(type) {
	case []any:
		return len(b)
	case obj:
		n, found := 0, false
		for _, e := range b {
			if !strings.HasSuffix(e.K, "_count") {
				continue
			}
			if v, isInt := e.V.(int); isInt {
				n += v
				found = true
			}
		}
		if found {
			return n
		}
	}
	return nil
}

func (s *Server) callTool(ctx context.Context, name string, a args) (toolResult, error) {
	switch name {
	case "search_legislation":
		return s.searchLegislation(ctx, a)
	case "get_law":
		return s.getLaw(ctx, a)
	case "get_article":
		return s.getArticle(ctx, a)
	case "get_law_texts":
		return s.getLawTexts(ctx, a)
	case "get_ruling":
		return s.getRuling(ctx, a)
	case "get_gazette_issue":
		return s.getGazetteIssue(ctx, a)
	case "get_links":
		return s.getLinks(ctx, a)
	case "list_legislation":
		return s.listLegislation(ctx, a)
	case "list_gazette_issues":
		return s.listGazetteIssues(ctx, a)
	case "possibly_affected":
		return s.possiblyAffected(ctx, a)
	}
	return fail("unknown tool"), nil
}
