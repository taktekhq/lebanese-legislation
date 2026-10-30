package site

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	mcpDefaultProtocol = "2025-06-18"
	mcpMaxBody         = 64 * 1024
	mcpCallTimeout     = 5 * time.Second
	mcpMaxListItems    = 500
	mcpCacheTTL        = 60 * time.Second
	mcpSemaphoreWait   = 2 * time.Second
)

var mcpSupportedProtocols = map[string]bool{"2025-06-18": true, "2025-03-26": true}

func (s *Server) mcpRoutes() {
	if s.isStatic || s.mcpRegistered {
		return
	}
	s.mcpRegistered = true
	s.mux.HandleFunc("POST /mcp", s.mcpPost)
	s.mux.HandleFunc("POST /mcp/", s.mcpPost)
	s.mux.HandleFunc("OPTIONS /mcp", s.mcpOptions)
	s.mux.HandleFunc("OPTIONS /mcp/", s.mcpOptions)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
)

func rpcErrorResponse(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: code, Message: msg}}
}

func rpcResultResponse(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func isNotification(raw json.RawMessage) bool { return len(raw) == 0 }

func mcpCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id, Mcp-Protocol-Version, Authorization")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id, Mcp-Protocol-Version")
}

func (s *Server) mcpOptions(w http.ResponseWriter, r *http.Request) {
	mcpCORS(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mcpGet(w http.ResponseWriter, r *http.Request) {
	mcpCORS(w)
	w.Header().Set("Allow", "POST, OPTIONS")
	http.Error(w, "this MCP server is stateless: POST a JSON-RPC request, there is no SSE stream to open", http.StatusMethodNotAllowed)
}

func (s *Server) mcpPost(w http.ResponseWriter, r *http.Request) {
	mcpCORS(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Mcp-Protocol-Version", mcpDefaultProtocol)

	ip := clientIP(r)
	if wait, ok := s.ipLim.Allow(ip); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
		http.Error(w, "too many requests, slow down", http.StatusTooManyRequests)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, mcpMaxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeRPC(w, http.StatusRequestEntityTooLarge, rpcErrorResponse(nil, rpcInvalidRequest, "request body too large"))
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, http.StatusBadRequest, rpcErrorResponse(nil, rpcParseError, "invalid JSON"))
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		writeRPC(w, http.StatusBadRequest, rpcErrorResponse(req.ID, rpcInvalidRequest, "not a JSON-RPC 2.0 request"))
		return
	}

	release, ok := s.dbSem.acquire(r.Context(), mcpSemaphoreWait)
	if !ok {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "the server is busy, try again shortly", http.StatusServiceUnavailable)
		return
	}
	defer release()

	if isNotification(req.ID) {

		s.handleRPC(r, req)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	resp, status := s.handleRPC(r, req)
	writeRPC(w, status, resp)
}

func writeRPC(w http.ResponseWriter, status int, resp rpcResponse) {
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(resp)
}

func (s *Server) handleRPC(r *http.Request, req rpcRequest) (rpcResponse, int) {
	switch req.Method {
	case "initialize":
		return rpcResultResponse(req.ID, s.mcpInitialize(req.Params)), http.StatusOK
	case "notifications/initialized":
		return rpcResponse{JSONRPC: "2.0"}, http.StatusAccepted
	case "ping":
		return rpcResultResponse(req.ID, map[string]any{}), http.StatusOK
	case "tools/list":
		return rpcResultResponse(req.ID, map[string]any{"tools": toolDefs}), http.StatusOK
	case "tools/call":
		return s.mcpToolsCall(r, req)
	default:
		return rpcErrorResponse(req.ID, rpcMethodNotFound, "unknown method "+req.Method), http.StatusOK
	}
}

func (s *Server) mcpInitialize(params json.RawMessage) map[string]any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	version := mcpDefaultProtocol
	if mcpSupportedProtocols[p.ProtocolVersion] {
		version = p.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"serverInfo":      map[string]any{"name": "lebaneselegislation", "version": "1.0.0"},
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"instructions": "Lebanese Legislation: every law, article, court ruling and Official Gazette issue " +
			"published by the Lebanese University's Centre for Research and Studies in Legal Informatics. " +
			"All text returned is copied verbatim from that database; nothing here is written or rewritten " +
			"by a model. " + notLegalAdvice,
	}
}

type mcpCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) mcpToolsCall(r *http.Request, req rpcRequest) (rpcResponse, int) {
	var p mcpCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
		return rpcErrorResponse(req.ID, rpcInvalidParams, "tools/call needs a tool name"), http.StatusOK
	}
	if _, ok := toolNames[p.Name]; !ok {
		return rpcErrorResponse(req.ID, rpcInvalidParams, "unknown tool "+p.Name), http.StatusOK
	}
	args := p.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	if cached, ok := s.ttlLRU.get(p.Name, args); ok {
		return rpcResultResponse(req.ID, cached), http.StatusOK
	}

	ctx, cancel := context.WithTimeout(r.Context(), mcpCallTimeout)
	defer cancel()
	result, isToolErr, err := s.callTool(ctx, p.Name, args)
	if err != nil {
		log.Printf("mcp tools/call %s: %v", p.Name, err)
		return rpcErrorResponse(req.ID, rpcInternalError, "internal error"), http.StatusOK
	}
	toolResult := map[string]any{
		"content": []map[string]any{{"type": "text", "text": mustJSON(result)}},
		"isError": isToolErr,
	}
	if !isToolErr {
		s.ttlLRU.set(p.Name, args, toolResult)
	}
	return rpcResultResponse(req.ID, toolResult), http.StatusOK
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"could not encode result"}`
	}
	return string(b)
}

func withAdvice(v any) map[string]any {
	return map[string]any{"result": v, "not_legal_advice": notLegalAdvice}
}

func toolErr(msg string) map[string]any { return map[string]any{"error": msg} }

var toolDefs = []map[string]any{
	{
		"name": "search_legislation",
		"description": "Search Lebanese legislation (laws, decrees, decisions), their articles, court rulings, or " +
			"Official Gazette issues, by keyword. Arabic and French. Returns verbatim snippets from the Lebanese " +
			"University's database, never an AI-written summary. English words work too (read into Arabic, or matched " +
			"against the unofficial English titles; searched_instead says what was searched when the typed words found " +
			"nothing). title_en is an unofficial English title a model wrote from the Arabic one: the Arabic title and " +
			"text are official and more accurate. Not legal advice.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "words to search for, Arabic, English or French"},
				"kind": map[string]any{"type": "string", "enum": []string{"laws", "articles", "rulings", "gazette"},
					"description": "narrow to one category; omit to search all four"},
				"year": map[string]any{"type": "integer", "description": "only laws issued this year (applies when kind is \"laws\" or omitted)"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "default": 10,
					"description": "results per category, at most 20"},
			},
			"required": []string{"query"},
		},
	},
	{
		"name": "get_law",
		"description": "Get a law, decree or similar text's metadata (kind, number, date, status), its table of " +
			"contents (article numbers) and the texts it amended or was amended by. Use get_article to read one " +
			"article's verbatim text. Not legal advice.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "integer", "description": "the law's numeric id, from search_legislation or its URL"}},
			"required":   []string{"id"},
		},
	},
	{
		"name": "get_article",
		"description": "Get one article's verbatim Arabic text (and French, when the Centre published it), its " +
			"in-force status, amendment history, and the canonical and source URLs. The text is copied exactly " +
			"from the database; nothing here is AI-written. Not legal advice.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"law_id": map[string]any{"type": "integer", "description": "the law's id (see get_law)"},
				"number": map[string]any{"type": "string", "description": "the article's printed number, e.g. \"212\" or \"12 مكرر\""},
			},
			"required": []string{"law_id", "number"},
		},
	},
	{
		"name": "get_ruling",
		"description": "Get one court ruling (judgment) by id: court, number, date, panel, and its verbatim text " +
			"when the Centre holds it as typed text (some rulings exist only as a scanned file), and the full judgment: " +
			"the scan's address and, once read, its pages as a machine reading that may contain errors (the scan is the reference). Not legal advice.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "integer", "description": "the ruling's numeric id, from search_legislation"}},
			"required":   []string{"id"},
		},
	},
	{
		"name": "get_gazette_issue",
		"description": "Get one Official Gazette issue by id: its number, publication date, and the laws, decrees " +
			"and decisions it published. Not legal advice.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "integer", "description": "the issue's numeric id, from search_legislation"}},
			"required":   []string{"id"},
		},
	},
}

var toolNames = func() map[string]bool {
	m := make(map[string]bool, len(toolDefs))
	for _, t := range toolDefs {
		m[t["name"].(string)] = true
	}
	return m
}()

func (s *Server) callTool(ctx context.Context, name string, args json.RawMessage) (any, bool, error) {
	switch name {
	case "search_legislation":
		return s.toolSearch(ctx, args)
	case "get_law":
		return s.toolGetLaw(ctx, args)
	case "get_article":
		return s.toolGetArticle(ctx, args)
	case "get_ruling":
		return s.toolGetRuling(ctx, args)
	case "get_gazette_issue":
		return s.toolGetGazette(ctx, args)
	default:
		return toolErr("unknown tool"), true, nil
	}
}

type mcpSearchArgs struct {
	Query string `json:"query"`
	Kind  string `json:"kind,omitempty"`
	Year  int    `json:"year,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

func (s *Server) toolSearch(ctx context.Context, raw json.RawMessage) (any, bool, error) {
	var a mcpSearchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return toolErr("invalid arguments: " + err.Error()), true, nil
	}
	q := strings.TrimSpace(a.Query)
	if q == "" || len([]rune(q)) >= 200 {
		return withAdvice(apiSearchResult{Q: q}), false, nil
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}
	out := apiSearchResult{Q: q}
	var err error
	switch a.Kind {
	case "laws":
		err = s.apiFindLaws(ctx, q, a.Year, limit, 0, &out)
	case "articles":
		err = s.apiFindArticles(ctx, q, limit, 0, &out)
	case "rulings":
		err = s.apiFindRulings(ctx, q, limit, 0, &out)
	case "gazette":
		err = s.apiFindGazette(ctx, q, limit, 0, &out)
	case "":
		err = s.apiFindAll(ctx, q, a.Year, limit, 0, &out)
	default:
		return toolErr(`kind must be one of "laws", "articles", "rulings", "gazette", or omitted`), true, nil
	}
	if err != nil {
		return nil, false, err
	}
	s.apiRescue(ctx, q, a.Kind, a.Year, 0, limit, &out)
	if a.Kind == "" || a.Kind == "laws" {
		s.apiFindLawsEn(ctx, q, a.Year, limit, 0, &out)
	}
	return withAdvice(out), false, nil
}

func unmarshalID(raw json.RawMessage) (int, bool) {
	var a struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.ID <= 0 {
		return 0, false
	}
	return a.ID, true
}

func mcpTruncateLaw(out *apiLawDetail) {
	if len(out.Articles) > mcpMaxListItems {
		out.Articles = out.Articles[:mcpMaxListItems]
	}
	if len(out.TableOfContents) > mcpMaxListItems {
		out.TableOfContents = out.TableOfContents[:mcpMaxListItems]
	}
}

func (s *Server) toolGetLaw(ctx context.Context, raw json.RawMessage) (any, bool, error) {
	id, ok := unmarshalID(raw)
	if !ok {
		return toolErr("id must be a positive integer"), true, nil
	}
	_, out, err := s.lawDetail(ctx, id)
	if err != nil {
		if errorIsNoRows(err) {
			return toolErr(fmt.Sprintf("law %d not found", id)), true, nil
		}
		return nil, false, err
	}
	mcpTruncateLaw(&out)
	return withAdvice(out), false, nil
}

func (s *Server) toolGetArticle(ctx context.Context, raw json.RawMessage) (any, bool, error) {
	var a struct {
		LawID  int    `json:"law_id"`
		Number string `json:"number"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.LawID <= 0 || strings.TrimSpace(a.Number) == "" {
		return toolErr("law_id (a positive integer) and number are required"), true, nil
	}
	_, art, out, err := s.articleDetail(ctx, a.LawID, a.Number)
	if err != nil {
		if errorIsNoRows(err) {
			return toolErr(fmt.Sprintf("law %d not found", a.LawID)), true, nil
		}
		return nil, false, err
	}
	if art == nil {
		return toolErr(fmt.Sprintf("article %q not found in law %d", a.Number, a.LawID)), true, nil
	}
	return withAdvice(out), false, nil
}

func (s *Server) toolGetRuling(ctx context.Context, raw json.RawMessage) (any, bool, error) {
	id, ok := unmarshalID(raw)
	if !ok {
		return toolErr("id must be a positive integer"), true, nil
	}
	out, err := s.rulingDetail(ctx, id)
	if err != nil {
		if errorIsNoRows(err) {
			return toolErr(fmt.Sprintf("ruling %d not found", id)), true, nil
		}
		return nil, false, err
	}
	return withAdvice(out), false, nil
}

func (s *Server) toolGetGazette(ctx context.Context, raw json.RawMessage) (any, bool, error) {
	id, ok := unmarshalID(raw)
	if !ok {
		return toolErr("id must be a positive integer"), true, nil
	}
	out, err := s.gazetteDetail(ctx, id)
	if err != nil {
		if errorIsNoRows(err) {
			return toolErr(fmt.Sprintf("gazette issue %d not found", id)), true, nil
		}
		return nil, false, err
	}
	return withAdvice(out), false, nil
}
