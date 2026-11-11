package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// The Worker's transport, JSON-RPC, analytics and calculated-amount tests
// that need no database.

func noDBServer(cfg Config) *Server { return newServer(noDB{}, cfg) }

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	return m
}

func TestTransport(t *testing.T) {
	s := noDBServer(Config{})
	get := func(method, path string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "https://lebaneselegislation.com"+path, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	if rec := get(http.MethodOptions, "/mcp", nil); rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("OPTIONS: %d %v", rec.Code, rec.Header())
	}
	rec := get(http.MethodGet, "/mcp", map[string]string{"CF-Connecting-IP": "203.0.113.2"})
	if rec.Code != 405 || !strings.Contains(rec.Header().Get("Allow"), "POST") || decode(t, rec)["error"] == nil {
		t.Errorf("GET: %d %s", rec.Code, rec.Body.String())
	}
	rec = get(http.MethodGet, "/mcp", map[string]string{"Accept": "text/html,application/xhtml+xml,*/*;q=0.8"})
	if rec.Code != 302 || rec.Header().Get("Location") != "https://lebaneselegislation.com/ai/" {
		t.Errorf("browser GET: %d %v", rec.Code, rec.Header())
	}
	if rec := get(http.MethodGet, "/mcp", map[string]string{"Accept": "text/event-stream, text/html"}); rec.Code != 405 {
		t.Errorf("SSE probe: %d", rec.Code)
	}
	if rec := get(http.MethodPost, "/", nil); rec.Code != 404 {
		t.Errorf("another path: %d", rec.Code)
	}
	if rec := post(s, strings.Repeat("x", 70*1024), nil); rec.Code != 413 {
		t.Errorf("a huge body: %d", rec.Code)
	}
	rec = post(s, "{not json", nil)
	if rec.Code != 400 || decode(t, rec)["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Errorf("invalid JSON: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(s, map[string]any{"jsonrpc": "1.0", "id": 1, "method": "ping"}, nil); rec.Code != 400 {
		t.Errorf("JSON-RPC 1.0: %d", rec.Code)
	}
	rec = post(s, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, nil)
	if rec.Code != 202 || rec.Body.Len() != 0 {
		t.Errorf("a notification: %d %q", rec.Code, rec.Body.String())
	}
	if rec := post(s, map[string]any{"jsonrpc": "2.0", "id": nil, "method": "ping"}, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"id":null`) {
		t.Errorf("an explicit null id is a request: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(s, "", nil); rec.Code != 400 {
		t.Errorf("an empty body: %d", rec.Code)
	}
	if rec := post(s, "[]", nil); rec.Code != 400 {
		t.Errorf("a batch: %d", rec.Code)
	}
}

func TestRateLimit(t *testing.T) {
	s := noDBServer(Config{RateLimitPerMinute: 60})
	saw := false
	for i := 0; i < 70 && !saw; i++ {
		rec := post(s, map[string]any{"jsonrpc": "2.0", "id": i, "method": "ping"}, map[string]string{"CF-Connecting-IP": "203.0.113.8"})
		if rec.Code == 429 {
			saw = true
			if rec.Header().Get("Retry-After") == "" {
				t.Error("429 without Retry-After")
			}
		}
	}
	if !saw {
		t.Error("70 requests in a burst were never limited")
	}
	off := noDBServer(Config{RateLimitPerMinute: -1})
	for i := 0; i < 100; i++ {
		if rec := post(off, map[string]any{"jsonrpc": "2.0", "id": i, "method": "ping"}, nil); rec.Code != 200 {
			t.Fatalf("limit off: %d", rec.Code)
		}
	}
}

func rpc(t *testing.T, s *Server, body map[string]any) map[string]any {
	t.Helper()
	return decode(t, post(s, body, nil))
}

func TestJSONRPCMethods(t *testing.T) {
	s := noDBServer(Config{Logf: func(string, ...any) {}})
	r := rpc(t, s, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}})["result"].(map[string]any)
	if r["protocolVersion"] != "2025-06-18" || r["serverInfo"].(map[string]any)["name"] != "lebaneselegislation" || !strings.Contains(r["instructions"].(string), "Not legal advice") ||
		!strings.Contains(r["instructions"].(string), "calculated_current") {
		t.Errorf("initialize: %v", r)
	}
	for v, want := range map[string]string{"2025-03-26": "2025-03-26", "1999-01-01": "2025-06-18"} {
		r := rpc(t, s, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": v}})["result"].(map[string]any)
		if r["protocolVersion"] != want {
			t.Errorf("protocol %s: %v", v, r["protocolVersion"])
		}
	}
	if r := rpc(t, s, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "ping"}); r["error"] != nil || fmt.Sprint(r["result"]) != "map[]" {
		t.Errorf("ping: %v", r)
	}
	tools := rpc(t, s, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/list"})["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, x := range tools {
		tool := x.(map[string]any)
		names = append(names, tool["name"].(string))
		if len(tool["description"].(string)) <= 10 || tool["inputSchema"].(map[string]any)["type"] != "object" {
			t.Errorf("tool %v", tool["name"])
		}
	}
	sort.Strings(names)
	if strings.Join(names, " ") != "get_article get_gazette_issue get_law get_law_texts get_links get_ruling list_gazette_issues list_legislation possibly_affected search_legislation" {
		t.Errorf("tools: %v", names)
	}
	if r := rpc(t, s, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "nope"}); r["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Errorf("unknown method: %v", r)
	}
	if r := rpc(t, s, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": map[string]any{}}); r["error"].(map[string]any)["code"].(float64) != -32602 {
		t.Errorf("no tool name: %v", r)
	}
	if r := rpc(t, s, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "tools/call", "params": map[string]any{"name": "nope"}}); r["error"].(map[string]any)["message"] != "unknown tool nope" {
		t.Errorf("unknown tool: %v", r)
	}
	// a database failure is an internal error, never a tool answer
	if r := rpc(t, s, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{"name": "get_law", "arguments": map[string]any{"id": 1}}}); r["error"].(map[string]any)["code"].(float64) != -32603 {
		t.Errorf("internal error: %v", r)
	}
}

// The tools/list answer is tools.json as written, every tool has a title and
// says it only reads (clients such as ChatGPT ask the user to confirm any tool
// not marked read-only), and the calculated amounts guidance is in the three
// tools that return them.
func TestToolDefinitions(t *testing.T) {
	s := noDBServer(Config{})
	body := post(s, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}, nil).Body.String()
	if !strings.HasPrefix(body, `{"jsonrpc":"2.0","id":3,"result":{"tools":[{"name":"search_legislation","title":"Search Lebanese legislation","description":"When calculated_current is present`) {
		t.Errorf("tools/list starts %s", body[:200])
	}
	var list struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				Title       string          `json:"title"`
				Annotations map[string]bool `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Result.Tools) != 10 {
		t.Errorf("%d tools", len(list.Result.Tools))
	}
	want := map[string]bool{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
	for _, tool := range list.Result.Tools {
		if tool.Title == "" {
			t.Errorf("%s: no title", tool.Name)
		}
		if fmt.Sprint(tool.Annotations) != fmt.Sprint(want) {
			t.Errorf("%s: annotations %v, want %v", tool.Name, tool.Annotations, want)
		}
	}
	var defs []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(toolsJSON, &defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		calc := d.Name == "get_article" || d.Name == "get_law_texts" || d.Name == "search_legislation"
		if calc != strings.Contains(d.Description, "calculated_current") {
			t.Errorf("%s: guidance %v", d.Name, !calc)
		}
		if calc && (!strings.Contains(d.Description, "authority") || !strings.Contains(d.Description, "verbatim")) {
			t.Errorf("%s: guidance incomplete", d.Name)
		}
	}
}

// Tool-level argument errors need no database.
func TestArgumentErrors(t *testing.T) {
	s := noDBServer(Config{})
	for _, c := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"get_law", map[string]any{}, "id must be a positive integer"},
		{"get_law", map[string]any{"id": -3}, "id must be a positive integer"},
		{"get_law", map[string]any{"id": ""}, "id must be a positive integer"},
		{"get_law_texts", map[string]any{"law_id": 0}, "law_id must be a positive integer"},
		{"get_article", map[string]any{"law_id": 1}, "law_id (a positive integer) and number are required"},
		{"get_article", map[string]any{"number": "5"}, "law_id (a positive integer) and number are required"},
		{"get_ruling", map[string]any{"id": nil}, "id must be a positive integer"},
		{"get_ruling", map[string]any{"id": 1.5}, "ruling 1.5 not found"},
		{"get_gazette_issue", map[string]any{"id": "abc"}, "gazette issue abc not found"},
		{"get_links", map[string]any{"key": "law/x"}, `key must look like "law/<id>", "law/<id>/art/<number>", "ruling/<id>", "opinion/<id>", "research/<id>" or "gazette/<id>"`},
		{"get_links", map[string]any{"key": "law/1", "direction": "up"}, `direction must be "to" or "from"`},
		{"list_legislation", map[string]any{"since": "2025-13"}, `since must be a date as YYYY, YYYY-MM or YYYY-MM-DD (e.g. "2025" or "2025-05-16")`},
		{"list_gazette_issues", map[string]any{"since": "2026", "until": "2025"}, "since is after until"},
		{"search_legislation", map[string]any{"query": "x", "kind": nil}, `kind must be one of "laws", "articles", "rulings", "gazette", or omitted`},
		{"possibly_affected", map[string]any{}, "law_id must be a positive integer"},
	} {
		r := call(t, s, c.name, c.args)
		if !r.isError || r.result["error"] != c.want {
			t.Errorf("%s %v: %v", c.name, c.args, r.result)
		}
	}
	r := call(t, s, "search_legislation", map[string]any{"query": "  "})
	if r.isError || mustMarshal(r.result) != `{"article_count":0,"articles":[],"gazette":[],"gazette_count":0,"law_count":0,"laws":[],"q":"","ruling_count":0,"rulings":[]}` {
		t.Errorf("an empty query: %v", r.result)
	}
}

func TestNumberFormsTable(t *testing.T) {
	b, err := os.ReadFile("../../testdata/article_numbers.json")
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Cases []struct {
			Raw    string   `json:"raw"`
			Target []string `json:"target"`
			Want   []string `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Cases) < 40 {
		t.Fatalf("%d cases", len(table.Cases))
	}
	for _, c := range table.Cases {
		has := setOf(c.Target...)
		want, got := "", ""
		for _, w := range c.Want {
			if has[w] {
				want = w
				break
			}
		}
		for _, f := range numberForms(c.Raw) {
			if has[f] {
				got = f
				break
			}
		}
		if got != want {
			t.Errorf("%q in %v: %q, want %q", c.Raw, c.Target, got, want)
		}
	}
	if got := strings.Join(numberForms("19.1"), "|"); got != "19.1|19-1|19" {
		t.Errorf("19.1: %s", got)
	}
}

func TestClientKind(t *testing.T) {
	want := map[string][]string{
		"assistant": {"Claude-User", "ChatGPT-User/1.0", "Perplexity-User/1.0", "MistralAI-User/1.0", "openai-mcp/1.0.0", "Google",
			"claude-code/2.1.319 (claude-vscode, agent-sdk/0.3.319)", "codex-mcp-client/0.166.0-alpha.5.1"},
		"internal": {"livecheck/0.1.0 (+https://github.com/taktekhq/livecheck)", "lebleg-db-prod-check/1", "taktekbot-latency-check",
			"lebleg-check-corrections", "lebaneselegislation-mcp-parity/1.0", "taktekbot-lebleg-list-check"},
		"registry": {"mcp-index/0.1 (+https://github.com/0xQuez/mcp-index)", "KoskamoDirectoryCheck/1.0 (+https://koskamo.com/explore)",
			"AgentToolIndex/1.0 (+https://agenttoolindex.com/method; passive MCP checks: initialize and", "allagents-verify/1 (+https://allagents.app/badges)"},
		"monitor": {
			"SentinelOracle/0.1 (+https://glimind.com/opt-out; liveness-only, never invokes tools)", "mcpbeat/0.1 (+https://mcpbeat.com/bot/; liveness check)",
			"AgentRankProbe/0.1", "rokmcp-collector/0.2 (+https://rokmcp.com/bot)", "agent-collector/0.1 (+health check; no payment is made)",
			"EveryMCP-Probe/1.0", "FraywickBot/1.0 (+https://fraywick.xyz/docs#bot)", "MCPWatch/0.1.0 longitudinal MCP security research",
			"reliability-bureau-spike/0.1", "LEGION-tlog/1.0", "TaifoonHarvester/1.0.0", "mcp-rugpull-research/1.0",
			"histor/0.1.0 (+https://histor.modelmarket.dev/#crawler; read-only: initialize + tools/list", "atpp-scan/1.0",
			"mcplantern-check/0.2", "AnnalistChecks/0.1 (+https://annalist.dev; independent MCP server checks)", "evyAI MCP Check/1.0 (+https://evyus.com/tools/mcp-check/)"},
		"library": {"node", "undici", "Python/3.11 aiohttp/3.14.5", "python-httpx/0.28.1", "Go-http-client/2.0", "curl/8.7.1", ""},
		"other":   {"Enerlio GmbH FACTANKER marc@enerlio.de", "Malaf/0.1 (+legal information service)", "coda-agent-runtime"},
	}
	for kind, uas := range want {
		for _, ua := range uas {
			if k := clientKind(ua, "", false); k != kind {
				t.Errorf("%q: %s, want %s", ua, k, kind)
			}
		}
	}
	if clientKind("node", "claude-ai", false) != "assistant" || clientKind("node", "cursor-vscode", false) != "assistant" {
		t.Error("assistant client names")
	}
	if clientKind("Talaria", "talaria-catalog", false) != "registry" {
		t.Error("registry client name")
	}
	// The repo's .mcp.json header makes our own Claude Code sessions internal.
	if clientKind("claude-code/2.1.319 (claude-vscode, agent-sdk/0.3.319)", "", true) != "internal" {
		t.Error("internal header")
	}
	for _, k := range []string{"monitor", "registry", "internal"} {
		if !notPerson(k) {
			t.Errorf("%s should not make a person", k)
		}
	}
	if notPerson("assistant") || notPerson("other") {
		t.Error("assistant and other are people")
	}
}

// calculatedCurrent accepts only a record that matches the article exactly.
func TestCalculatedCurrentValidation(t *testing.T) {
	const source = "تحدد القيمة بمبلغ 100000 ليرة لبنانية"
	const calculated = "تحدد القيمة بمبلغ 5000000 ليرة لبنانية (خمسة ملايين ليرة لبنانية)"
	sum := sha256.Sum256([]byte(source))
	hash := hex.EncodeToString(sum[:])
	record := func(patch map[string]any) string {
		r := map[string]any{"kind": "statutory_monetary_adjustment", "text_ar": calculated, "source_text_ar": source, "source_sha256": hash,
			"is_published_wording": false, "multiplier": 50,
			"authority": map[string]any{"law_id": 330819, "article": "1", "url": "https://lebaneselegislation.com/decree/330819/article/1/", "effective_on": "2024-09-19"}}
		for k, v := range patch {
			r[k] = v
		}
		b, _ := json.Marshal(r)
		return string(b)
	}
	sp := func(s string) *string { return &s }
	base := func() articleRow {
		return articleRow{LawID: 244565, Number: "86", TextAr: "نص المركز السابق", CurrentTextAr: sp(source), Version: "2.0", State: "in_force", ValidFrom: sp("2024-09-19"), Calculated: sp(record(nil))}
	}
	today := "2026-11-01"
	a := base()
	got := calculatedCurrent(&a, "in_force", today)
	want := `{"kind":"statutory_monetary_adjustment","text_ar":"` + calculated + `","source_text_ar":"` + source + `","source_sha256":"` + hash +
		`","is_published_wording":false,"multiplier":50,"authority":{"law_id":330819,"article":"1","url":"https://lebaneselegislation.com/decree/330819/article/1/","effective_on":"2024-09-19"}}`
	if mustMarshal(got) != want {
		t.Errorf("valid record: %s", mustMarshal(got))
	}
	if txt := articleTexts(&a, false); mustMarshal(txt) != `{"text_ar":"`+source+`","version":"2.0","current_since":"2024-09-19","centre_text_ar":"نص المركز السابق","repealed":false}` {
		t.Errorf("published wording unchanged: %s", mustMarshal(txt))
	}
	for name, change := range map[string]func(*articleRow, *string){
		"stale source":            func(a *articleRow, _ *string) { a.CurrentTextAr = sp("تغير النص") },
		"repealed article":        func(a *articleRow, _ *string) { a.State = "repealed" },
		"annulled article":        func(a *articleRow, _ *string) { a.State = "annulled" },
		"pending wording":         func(a *articleRow, _ *string) { a.Pending = true },
		"future current wording":  func(a *articleRow, _ *string) { a.ValidFrom = sp("2999-01-01") },
		"future original wording": func(a *articleRow, _ *string) { a.InForceFrom = sp("2999-01-01") },
		"repealed law":            func(_ *articleRow, st *string) { *st = "repealed" },
		"annulled law":            func(_ *articleRow, st *string) { *st = "annulled" },
		"other law":               func(a *articleRow, _ *string) { a.LawID = 100 },
		"no record":               func(a *articleRow, _ *string) { a.Calculated = nil },
	} {
		a := base()
		status := "in_force"
		change(&a, &status)
		if c := calculatedCurrent(&a, status, today); c != nil {
			t.Errorf("%s: %s", name, mustMarshal(c))
		}
	}
	for name, patch := range map[string]map[string]any{
		"wrong hash": {"source_sha256": strings.Repeat("0", 64)}, "wrong source": {"source_text_ar": "stale"}, "wrong multiplier": {"multiplier": 5},
		"published claim": {"is_published_wording": true}, "wrong kind": {"kind": "other"}, "empty calculation": {"text_ar": ""},
		"wrong authority": {"authority": map[string]any{"law_id": 330819}},
	} {
		a := base()
		a.Calculated = sp(record(patch))
		if c := calculatedCurrent(&a, "in_force", today); c != nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, raw := range []string{"{", "null", "[]", "42"} {
		a := base()
		a.Calculated = sp(raw)
		if c := calculatedCurrent(&a, "in_force", today); c != nil {
			t.Errorf("malformed %s accepted", raw)
		}
	}
	a = base()
	a.CurrentTextAr, a.TextAr = nil, source
	if calculatedCurrent(&a, "in_force", today) == nil {
		t.Error("the original source when no current text")
	}
	if calculatedCurrent(&a, "in_force", "2024-09-18") != nil {
		t.Error("before the decree's effective date")
	}
}

// ---------------------------------------------------------------- analytics

const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36"

func trackedServer(rt *recorder) *Server {
	return noDBServer(Config{
		GAMCPMeasurementID: "G-MCP", GAMCPAPISecret: "mcp-secret", GAHFMeasurementID: "G-HF", GAHFAPISecret: "hf-secret",
		PostHogKey: "phc_test", PostHogHost: "https://ph.example", HTTPClient: &http.Client{Transport: rt}, HuggingFaceAPI: "https://hf.example/api",
		Logf: func(string, ...any) {},
		Rand: func() float64 { return 0 }, // every sampled event is sent
	})
}

func TestNothingSentWithoutKeys(t *testing.T) {
	rt := &recorder{}
	s := noDBServer(Config{HTTPClient: &http.Client{Transport: rt}})
	post(s, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, nil)
	call(t, s, "get_law", map[string]any{"id": -1})
	s.Flush()
	if len(rt.reqs) != 0 {
		t.Errorf("sent %d requests without keys", len(rt.reqs))
	}
}

func phEvents(rt *recorder) []map[string]any {
	var out []map[string]any
	for _, r := range rt.sent("https://ph.example/batch/") {
		for _, e := range r.body["batch"].([]any) {
			out = append(out, e.(map[string]any))
		}
	}
	return out
}

func gaEvents(rt *recorder) []map[string]any {
	var out []map[string]any
	for _, r := range rt.sent(gaEndpoint) {
		for _, e := range r.body["events"].([]any) {
			out = append(out, e.(map[string]any))
		}
	}
	return out
}

func TestPostHogEvents(t *testing.T) {
	rt := &recorder{}
	s := trackedServer(rt)
	post(s, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "claude-ai", "version": "0.1"}}},
		map[string]string{"User-Agent": "node"})
	s.Flush()
	ph := phEvents(rt)
	if len(ph) != 1 || ph[0]["event"] != "$mcp_initialize" {
		t.Fatalf("initialize: %v", ph)
	}
	p := ph[0]["properties"].(map[string]any)
	if p["$mcp_client_name"] != "claude-ai" || p["$mcp_client_version"] != "0.1" || p["$mcp_server_name"] != "lebaneselegislation" || p["client_kind"] != "assistant" ||
		p["$ip"] != "203.0.113.1" || p["$lib"] != "lebaneselegislation-worker" || p["distinct_id"] != visitorID("", "203.0.113.1", "node") {
		t.Errorf("initialize properties: %v", p)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(fmt.Sprint(p["$session_id"])) {
		t.Errorf("session id %v", p["$session_id"])
	}
	if gas := gaEvents(rt); len(gas) != 1 || gas[0]["name"] != "mcp_initialize" || gas[0]["params"].(map[string]any)["client_name"] != "claude-ai" ||
		gas[0]["params"].(map[string]any)["page_location"] != mcpPage {
		t.Errorf("GA initialize: %v", gas)
	}

	rt2 := &recorder{}
	s = trackedServer(rt2)
	post(s, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}, nil)
	post(s, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "nope"}}, nil)
	call(t, s, "get_law", map[string]any{"id": -1})
	post(s, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "ping"}, nil)
	post(s, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "ping"}, map[string]string{"User-Agent": "SentinelOracle/0.1 (liveness-only)"})
	s.Flush()
	ph = phEvents(rt2)
	if len(ph) != 3 {
		t.Fatalf("events: %v", ph)
	}
	sort.Slice(ph, func(i, j int) bool { return fmt.Sprint(ph[i]["event"]) < fmt.Sprint(ph[j]["event"]) })
	var list, unknown, invalid map[string]any
	for _, e := range ph {
		pr := e["properties"].(map[string]any)
		switch {
		case e["event"] == "mcp_tools_list":
			list = pr
		case pr["$mcp_tool_name"] == "nope":
			unknown = pr
		default:
			invalid = pr
		}
	}
	if !strings.Contains(fmt.Sprint(list["$mcp_listed_tool_names"]), "search_legislation") {
		t.Errorf("tools/list: %v", list)
	}
	if unknown["$mcp_is_error"] != true || unknown["$mcp_error_type"] != "validation" {
		t.Errorf("unknown tool: %v", unknown)
	}
	if invalid["$mcp_is_error"] != true || invalid["$mcp_error_type"] != "validation" || invalid["$mcp_tool_name"] != "get_law" ||
		!strings.Contains(fmt.Sprint(invalid["$mcp_response"]), "positive integer") || invalid["$mcp_cache_hit"] != false {
		t.Errorf("a rejected argument: %v", invalid)
	}
	if _, isNum := invalid["$mcp_duration_ms"].(float64); !isNum {
		t.Errorf("duration: %v", invalid["$mcp_duration_ms"])
	}
	var calls int
	for _, e := range gaEvents(rt2) {
		if e["name"] == "mcp_tool_call" {
			calls++
		}
	}
	if calls != 2 {
		t.Errorf("GA tool calls: %d", calls)
	}

	// a monitor is tagged and makes no person
	rt3 := &recorder{}
	s = trackedServer(rt3)
	post(s, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, map[string]string{"User-Agent": "mcpbeat/0.1 (+https://mcpbeat.com/bot/; liveness check)"})
	post(s, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}, map[string]string{"User-Agent": "Claude-User"})
	s.Flush()
	for _, e := range phEvents(rt3) {
		p := e["properties"].(map[string]any)
		if p["$user_agent"] == "Claude-User" {
			if p["client_kind"] != "assistant" || p["$process_person_profile"] != nil {
				t.Errorf("assistant: %v", p)
			}
		} else if p["client_kind"] != "monitor" || p["$process_person_profile"] != false {
			t.Errorf("monitor: %v", p)
		}
	}
}

// Liveness monitors only connect and list tools, thousands of times a week:
// PostHog gets one handshake in ten, marked with its sample_rate so a chart
// can count it ten times. Everything else is always sent, with no rate.
func TestMonitorHandshakesAreSampled(t *testing.T) {
	const monitor, person = "mcpbeat/0.1 (+https://mcpbeat.com/bot/; liveness check)", "Claude-User"
	initialize := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}}
	list := map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}
	tool := map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "nope"}}
	run := func(draw float64) (monitors, people []map[string]any, ga int) {
		rt := &recorder{}
		s := trackedServer(rt)
		s.cfg.Rand = func() float64 { return draw }
		for _, ua := range []string{monitor, person} {
			for _, body := range []any{initialize, list, tool} {
				if rec := post(s, body, map[string]string{"User-Agent": ua}); rec.Code != http.StatusOK {
					t.Fatalf("%s: answered %d", ua, rec.Code)
				}
			}
		}
		s.Flush()
		for _, e := range phEvents(rt) {
			p := e["properties"].(map[string]any)
			p["event"] = e["event"]
			if p["$user_agent"] == monitor {
				monitors = append(monitors, p)
			} else {
				people = append(people, p)
			}
		}
		return monitors, people, len(gaEvents(rt))
	}

	// Drawn under the rate: all six events, the monitor's two handshakes weighted.
	monitors, people, ga := run(0.09)
	if len(monitors) != 3 || len(people) != 3 || ga != 6 {
		t.Fatalf("kept draw: %d monitor and %d other events, %d to GA4", len(monitors), len(people), ga)
	}
	for _, p := range monitors {
		want := any(0.1)
		if p["event"] == "$mcp_tool_call" {
			want = nil
		}
		if p["sample_rate"] != want || p["client_kind"] != "monitor" {
			t.Errorf("monitor %v: sample_rate %v, kind %v", p["event"], p["sample_rate"], p["client_kind"])
		}
	}
	for _, p := range people {
		if p["sample_rate"] != nil {
			t.Errorf("%v from an assistant carries sample_rate %v", p["event"], p["sample_rate"])
		}
	}

	// Drawn at or over the rate: the monitor's handshakes are left out, its
	// tool call and every event of the assistant still arrive.
	for _, draw := range []float64{0.1, 0.9} {
		monitors, people, ga = run(draw)
		if len(monitors) != 1 || monitors[0]["event"] != "$mcp_tool_call" || len(people) != 3 || ga != 6 {
			t.Errorf("draw %v: monitor events %v, %d other events, %d to GA4", draw, monitors, len(people), ga)
		}
	}

	// Only a monitor's handshakes: directories and our own tools are few.
	for _, kind := range []string{"assistant", "registry", "internal", "library", "other"} {
		for _, event := range []string{"$mcp_initialize", "mcp_tools_list", "$mcp_tool_call"} {
			if _, sampled := handshakeSampleRate(kind, event); sampled {
				t.Errorf("%s %s is sampled", kind, event)
			}
		}
	}
	if _, sampled := handshakeSampleRate("monitor", "mcp_rate_limited"); sampled {
		t.Error("a monitor's rate-limit refusal is sampled")
	}
}

func TestRateLimitedEvent(t *testing.T) {
	rt := &recorder{}
	s := trackedServer(rt)
	s.limiter, s.refusal = newRateLimiter(1), newRateLimiter(1)
	var statuses []int
	for i := 0; i < 3; i++ {
		statuses = append(statuses, post(s, map[string]any{"jsonrpc": "2.0", "id": i, "method": "ping"}, map[string]string{"CF-Connecting-IP": "203.0.113.30", "User-Agent": "curl/8"}).Code)
	}
	s.Flush()
	var refused []map[string]any
	for _, e := range phEvents(rt) {
		if e["event"] == "mcp_rate_limited" {
			refused = append(refused, e["properties"].(map[string]any))
		}
	}
	if fmt.Sprint(statuses) != "[200 429 429]" || len(refused) != 1 || refused[0]["client_kind"] != "library" || refused[0]["limit_per_minute"] != float64(1) {
		t.Errorf("statuses %v, refused %v", statuses, refused)
	}
}

func TestPostHogClientAndIDs(t *testing.T) {
	if a, b := visitorID("s1", "203.0.113.9", "UA"), visitorID("s2", "203.0.113.9", "UA"); a == b || len(a) != 64 || a != visitorID("s1", "203.0.113.9", "UA") {
		t.Error("visitor ids")
	}
	long := strings.Repeat("x", 20000)
	out := clipProps(obj{{"s", long}, {"n", 3}, {"o", map[string]any{"a": map[string]any{"b": long}}}, {"l", []any{long}}, {"u", nil}}, phMaxString)
	b := mustMarshal(out)
	if strings.Count(b, "x") != 30000 || strings.Contains(b, `"u"`) {
		t.Errorf("clipProps: %d x", strings.Count(b, "x"))
	}
	a := mcpSessionID(strings.Repeat("ab", 32), "2026-10-20")
	if a != mcpSessionID(strings.Repeat("ab", 32), "2026-10-20") || a == mcpSessionID(strings.Repeat("ab", 32), "2026-10-21") {
		t.Error("session ids")
	}
	if mcpErrorType(errTimeout) != "timeout" || mcpErrorType(fmt.Errorf("boom")) != "internal" || mcpErrorType(context.DeadlineExceeded) != "timeout" {
		t.Error("error types")
	}
	// PostHog answering 500 is logged, never returned
	rt := &recorder{answer: func(r *http.Request) *http.Response { return jsonResponse(r, 500, "{}") }}
	s := trackedServer(rt)
	if rec := post(s, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, nil); rec.Code != 200 {
		t.Errorf("status %d", rec.Code)
	}
	s.Flush()
}

func TestBadges(t *testing.T) {
	get := func(s *Server, path string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "https://lebaneselegislation.com"+path, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	rt := &recorder{}
	s := trackedServer(rt)
	rec := get(s, "/badge/mcp.svg?src=hf", map[string]string{"User-Agent": browserUA, "CF-Connecting-IP": "203.0.113.20", "Referer": "https://huggingface.co/"})
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "image/svg+xml") || !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") ||
		!strings.Contains(rec.Body.String(), `aria-label="MCP server: online"`) {
		t.Errorf("mcp badge: %d %v", rec.Code, rec.Header())
	}
	if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "cross-origin" {
		t.Errorf("badge CORP %q: browsers hide it on other sites", got)
	}
	get(s, "/badge/mcp.svg", map[string]string{"User-Agent": browserUA})
	get(s, "/badge/mcp.svg?src=hf", map[string]string{"User-Agent": "Googlebot-Image/1.0"})
	s.Flush()
	ga := rt.sent(gaEndpoint)
	if len(ga) != 1 || !strings.Contains(ga[0].url, "measurement_id=G-HF") {
		t.Fatalf("one view in the HF stream: %v", ga)
	}
	ev := ga[0].body["events"].([]any)[0].(map[string]any)
	p := ev["params"].(map[string]any)
	if ev["name"] != "page_view" || p["page_location"] != "https://lebaneselegislation.com/huggingface" || p["page_referrer"] != "https://huggingface.co/" || p["badge"] != "mcp" {
		t.Errorf("view: %v", ev)
	}
	ph := phEvents(rt)
	if len(ph) != 1 || ph[0]["event"] != "$pageview" || ph[0]["properties"].(map[string]any)["$current_url"] != "https://huggingface.co/datasets/taktekhq/lebanese-legislation" {
		t.Errorf("$pageview: %v", ph)
	}
	ghrt := &recorder{}
	gh := trackedServer(ghrt)
	get(gh, "/badge/legislation.svg?src=gh", map[string]string{"User-Agent": "github-camo (876de43e)"})
	gh.Flush()
	if ph := phEvents(ghrt); len(ph) != 1 || ph[0]["properties"].(map[string]any)["$current_url"] != "https://github.com/taktekhq/lebanese-legislation" {
		t.Errorf("README view: %v", ph)
	}
	if !isBot("") || isBot(browserUA) {
		t.Error("isBot")
	}
	if rec := get(s, "/badge/nope.svg", nil); rec.Code != 404 || !strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("unknown badge: %d", rec.Code)
	}
	// stats come from the database; without one the badge says n/a
	if rec := get(s, "/badge/legislation.svg", nil); !strings.Contains(rec.Body.String(), `aria-label="legislation: n/a"`) {
		t.Errorf("no database: %s", rec.Body.String())
	}

	down := trackedServer(&recorder{answer: func(r *http.Request) *http.Response { return jsonResponse(r, 503, "down") }})
	if rec := get(down, "/badge/updated.svg", nil); !strings.Contains(rec.Body.String(), `aria-label="last updated: n/a"`) {
		t.Errorf("HF down: %s", rec.Body.String())
	}
	hf := &recorder{answer: func(r *http.Request) *http.Response {
		return jsonResponse(r, 200, `{"id":"taktekhq/lebanese-legislation","lastModified":"2026-10-20T14:31:53.000Z"}`)
	}}
	up := trackedServer(hf)
	dl := trackedServer(&recorder{answer: func(r *http.Request) *http.Response { return jsonResponse(r, 200, `{"downloadsAllTime":12345}`) }})
	if rec := get(dl, "/badge/downloads.svg", nil); !strings.Contains(rec.Body.String(), `aria-label="Hugging Face downloads: 12,345"`) {
		t.Errorf("downloads: %s", rec.Body.String())
	}
	if rec := get(up, "/badge/updated.svg", nil); !strings.Contains(rec.Body.String(), `aria-label="last updated: 20 Oct 2026"`) {
		t.Errorf("HF up: %s", rec.Body.String())
	}
	get(up, "/badge/updated.svg", nil)
	if n := len(hf.sent("https://hf.example/api/datasets/taktekhq/lebanese-legislation?expand[]=lastModified")); n != 1 {
		t.Errorf("HF read %d times, cached for an hour", n)
	}
}

func TestBadgeStatsDB(t *testing.T) {
	s := dbServer(t)
	req := httptest.NewRequest(http.MethodGet, "https://lebaneselegislation.com/badge/legislation.svg", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if !regexp.MustCompile(`<svg[^>]*aria-label="legislation: [\d,]+ texts"`).MatchString(rec.Body.String()) {
		t.Errorf("legislation badge: %s", rec.Body.String())
	}
}

func TestHFStats(t *testing.T) {
	rt := &recorder{answer: func(r *http.Request) *http.Response {
		if strings.HasPrefix(r.URL.String(), "https://hf.example/") {
			return jsonResponse(r, 200, `{"downloads":12,"downloadsAllTime":40,"likes":3}`)
		}
		return jsonResponse(r, 200, `{}`)
	}}
	s := trackedServer(rt)
	if err := s.SendHFStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Flush()
	ph := phEvents(rt)
	if len(ph) != 1 || ph[0]["event"] != "hf_dataset_stats" {
		t.Fatalf("events: %v", ph)
	}
	p := ph[0]["properties"].(map[string]any)
	if p["downloads_30d"] != float64(12) || p["downloads_all_time"] != float64(40) || p["likes"] != float64(3) || p["dataset"] != HFDataset || p["distinct_id"] != "lebaneselegislation-hf-stats" {
		t.Errorf("properties: %v", p)
	}
	if err := noDBServer(Config{}).SendHFStats(context.Background()); err != nil {
		t.Errorf("PostHog off: %v", err)
	}
}

func TestResultCache(t *testing.T) {
	c := newResultCache(50*time.Millisecond, 2)
	c.put("a", "1", 1)
	if e, ok := c.get("a"); !ok || e.text != "1" {
		t.Error("a hit")
	}
	time.Sleep(60 * time.Millisecond)
	if _, ok := c.get("a"); ok {
		t.Error("an expired entry")
	}
	c.put("b", "2", nil)
	c.put("c", "3", nil)
	c.put("d", "4", nil)
	if len(c.entries) > 2 {
		t.Errorf("%d entries over the cap", len(c.entries))
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := map[string]string{"GA_MCP_MEASUREMENT_ID": "G-1", "GA_MCP_API_SECRET": "s", "POSTHOG_KEY": "phc", "MCP_RATE_LIMIT": "-1"}
	c := ConfigFromEnv(func(k string) string { return env[k] })
	if c.GAMCPMeasurementID != "G-1" || c.GAMCPAPISecret != "s" || c.PostHogKey != "phc" || c.RateLimitPerMinute != -1 || c.GAHFMeasurementID != "" {
		t.Errorf("%+v", c)
	}
	if s := newServer(noDB{}, c); s.limiter != nil {
		t.Error("the limit is off")
	}
}

// Without a database answer the server reads the computed layout and asks
// again next time, so a failed check never pins it.
func TestLayoutFallsBack(t *testing.T) {
	s := newServer(noDB{}, Config{RateLimitPerMinute: -1})
	if got := s.sql(context.Background()); got != &computed {
		t.Errorf("layout %s, want computed", got.name)
	}
	if s.layout.Load() != nil {
		t.Error("a failed check was remembered")
	}
	if s.pubCounts(context.Background()) || s.pubCount.Load() != nil {
		t.Error("law_pub_count: a failed check was read or remembered")
	}
}
