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

// callRecord is what one JSON-RPC request did, for PostHog ($mcp_*
// properties, the shape PostHog's MCP analytics screens read).
type callRecord struct {
	event string
	props obj
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

// handleRPC answers one JSON-RPC request and appends what happened to
// events, for GA4, and to rec, for PostHog.
func (s *Server) handleRPC(r *http.Request, req rpcRequest, events *[]gaEvent, rec *callRecord) obj {
	switch req.methodName() {
	case "initialize":
		p := objectParams(req.params)
		info, _ := p["clientInfo"].(map[string]any)
		ev := gaEvent{name: "mcp_initialize", params: obj{
			{"client_name", optString(info["name"])}, {"client_version", optString(info["version"])}, {"protocol_version", optString(p["protocolVersion"])},
		}}
		*events = append(*events, ev)
		rec.event = "$mcp_initialize"
		rec.props.set("$mcp_client_name", optString(info["name"]))
		rec.props.set("$mcp_client_version", optString(info["version"]))
		rec.props.set("$mcp_protocol_version", optString(p["protocolVersion"]))
		return rpcResult(req.id, initializeResult(p))
	case "ping":
		return rpcResult(req.id, obj{})
	case "tools/list":
		*events = append(*events, gaEvent{name: "mcp_tools_list", params: obj{}})
		rec.event = "mcp_tools_list"
		rec.props.set("$mcp_listed_tool_names", toolNames)
		return rpcResult(req.id, obj{{"tools", toolsCompact}})
	case "tools/call":
		return s.handleToolsCall(r.Context(), req, events, rec)
	}
	return rpcError(req.id, rpcMethodNotFound, "unknown method "+req.methodName())
}

// toolCallEvent describes one tools/call for GA4: which tool, whether it
// came from the cache or failed, and for searches what was searched.
func toolCallEvent(name string, a map[string]any, cacheHit, isError bool) gaEvent {
	p := obj{{"tool_name", name}, {"cache_hit", b2i(cacheHit)}, {"is_error", b2i(isError)}}
	if name == "search_legislation" {
		p.set("search_term", optString(a["query"]))
		p.set("search_kind", optString(a["kind"]))
	}
	return gaEvent{name: "mcp_tool_call", params: p}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Server) handleToolsCall(ctx context.Context, req rpcRequest, events *[]gaEvent, rec *callRecord) obj {
	p := objectParams(req.params)
	if !truthy(p["name"]) {
		return rpcError(req.id, rpcInvalidParams, "tools/call needs a tool name")
	}
	name := jsString(p["name"])
	a, isObj := p["arguments"].(map[string]any)
	if !isObj {
		a = map[string]any{}
	}
	rec.event = "$mcp_tool_call"
	rec.props.set("$mcp_tool_name", name)
	rec.props.set("$mcp_parameters", a)
	rec.props.set("$mcp_cache_hit", false)
	rec.props.set("$mcp_is_error", false)
	if !isKnownTool(name) {
		rec.props.set("$mcp_is_error", true)
		rec.props.set("$mcp_error_type", "validation")
		*events = append(*events, toolCallEvent(name, map[string]any{}, false, true))
		return rpcError(req.id, rpcInvalidParams, "unknown tool "+name)
	}
	argsJSON := mustMarshal(a)
	key := name + "\x00" + argsJSON
	if hit, found := s.results.get(key); found {
		*events = append(*events, toolCallEvent(name, a, true, false))
		rec.props.set("$mcp_cache_hit", true)
		rec.props.set("$mcp_response", hit.text)
		rec.props.set("$mcp_result_count", hit.count)
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
		rec.props.set("$mcp_is_error", true)
		rec.props.set("$mcp_error_type", mcpErrorType(err))
		rec.props.set("$mcp_error_message", "Error: "+err.Error())
		*events = append(*events, toolCallEvent(name, a, false, true))
		return rpcError(req.id, rpcInternalError, "internal error")
	}
	*events = append(*events, toolCallEvent(name, a, false, res.isError))
	text := mustMarshal(res.body)
	count := resultCount(res.body)
	rec.props.set("$mcp_response", text)
	rec.props.set("$mcp_result_count", count)
	if res.isError {
		rec.props.set("$mcp_is_error", true)
		rec.props.set("$mcp_error_type", "validation")
	} else {
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
