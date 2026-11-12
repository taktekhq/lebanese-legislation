package site

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func agentGet(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

// Every discovery file answers, as the type agents expect, and is kept a day.
func TestAgentDiscoveryFiles(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for path, typ := range map[string]string{
		"/.well-known/api-catalog":                                "application/linkset+json",
		"/.well-known/mcp/server-card.json":                       "application/json",
		"/.well-known/agent-card.json":                            "application/json",
		"/.well-known/agent-skills/index.json":                    "application/json",
		"/.well-known/agent-skills/lebanese-legislation/SKILL.md": "text/markdown",
		"/.well-known/ai-catalog.json":                            "application/json",
		"/.well-known/poppy.json":                                 "application/json",
	} {
		rec := agentGet(t, s, path)
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), typ) {
			t.Errorf("%s: %d %q, want 200 %s", path, rec.Code, rec.Header().Get("Content-Type"), typ)
			continue
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "public") || maxAge(cc) < 3600 {
			t.Errorf("%s: Cache-Control %q", path, cc)
		}
		if strings.Contains(typ, "json") {
			var v map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
				t.Errorf("%s: not JSON: %v", path, err)
			}
		}
	}

	var card struct {
		ServerInfo struct{ Version string }
		Transport  struct{ Type, Endpoint string }
		Tools      []struct{ Name, Title string }
	}
	if err := json.Unmarshal(agentGet(t, s, "/.well-known/mcp/server-card.json").Body.Bytes(), &card); err != nil {
		t.Fatal(err)
	}
	if card.Transport.Endpoint != Base+"/mcp" || card.Transport.Type != "streamable-http" || card.ServerInfo.Version == "" {
		t.Errorf("server card: %+v", card)
	}
	names := map[string]bool{}
	for _, tool := range card.Tools {
		names[tool.Name] = tool.Title != ""
	}
	if len(names) < 10 || !names["search_legislation"] || !names["get_article"] {
		t.Errorf("server card tools: %v", names)
	}

	// The skill index's digest is the SHA-256 of the file it names.
	var index struct {
		Skills []struct{ Name, URL, Digest string }
	}
	if err := json.Unmarshal(agentGet(t, s, "/.well-known/agent-skills/index.json").Body.Bytes(), &index); err != nil || len(index.Skills) != 1 {
		t.Fatalf("skill index: %v %+v", err, index)
	}
	skill := agentGet(t, s, index.Skills[0].URL)
	sum := sha256.Sum256(skill.Body.Bytes())
	if skill.Code != 200 || index.Skills[0].Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Errorf("skill %s: %d, digest %s", index.Skills[0].URL, skill.Code, index.Skills[0].Digest)
	}
	if strings.Contains(skill.Body.String(), "{SITE}") || !strings.HasPrefix(skill.Body.String(), "---\nname: lebanese-legislation\n") {
		t.Error("the skill file has an unfilled placeholder or no front matter")
	}
}

// A discovery file that names an address nobody answers sends an agent to a
// dead end. Every address on this site that the files, the Link header and
// the skill name has a handler of its own (not the catch-all).
func TestAgentFilesNameOnlyWhatExists(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.mcpRoutes()
	seen := map[string]bool{}
	check := func(where, path string) {
		path, _, _ = strings.Cut(path, "?")
		if path == "" || seen[path] || strings.Contains(path, "{") {
			return
		}
		seen[path] = true
		if path == "/mcp" || path == "/a2a" || path == "/healthz" { // POST endpoints and the health check
			return
		}
		if p := s.Route(path); p == "" || p == "GET /" || p == "GET /.well-known/" {
			t.Errorf("%s names %s, which nothing answers (route %q)", where, path, p)
		}
	}
	abs := regexp.MustCompile(regexp.QuoteMeta(Base) + `(/[^\s"'<>)\x60,]*)`)
	for path, f := range agentFiles {
		for _, m := range abs.FindAllStringSubmatch(string(f.body), -1) {
			check(path, strings.TrimRight(m[1], "."))
		}
	}
	for _, m := range regexp.MustCompile(`<(/[^>]*)>`).FindAllStringSubmatch(AgentLinks, -1) {
		check("the Link header", m[1])
	}
	if len(seen) < 12 {
		t.Errorf("only %d addresses checked: %v", len(seen), seen)
	}
}

// No sign-in exists, so no OAuth metadata is published: MCP clients would
// start a sign-in that goes nowhere (probes_test.go).
func TestAgentFilesPublishNoOAuth(t *testing.T) {
	for path := range agentFiles {
		if strings.Contains(path, "oauth") || strings.Contains(path, "openid") {
			t.Errorf("%s: OAuth metadata would tell MCP clients to sign in", path)
		}
	}
}

func TestRobotsContentSignals(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	body := agentGet(t, s, "/robots.txt").Body.String()
	groups := strings.Split(body, "\n\n")
	n := 0
	for _, g := range groups {
		if !strings.Contains(g, "User-agent: ") {
			continue
		}
		n++
		if strings.Count(g, "Content-Signal: search=yes, ai-input=yes, ai-train=yes") != 1 {
			t.Errorf("group without its Content-Signal line:\n%s", g)
		}
	}
	if n != len(aiCrawlers)+1 {
		t.Errorf("%d groups, want %d", n, len(aiCrawlers)+1)
	}
}

// Pages carry the Link header, and the Worker that serves the static copies
// sends the same one.
func TestAgentLinksMatchTheWorker(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := agentGet(t, s, "/en/privacy/").Header().Get("Link"); got != AgentLinks {
		t.Errorf("Link on a page = %q", got)
	}
	js, err := os.ReadFile("../../workers/static-site/src/headers.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range strings.Split(AgentLinks, ", ") {
		if !strings.Contains(string(js), "'"+link+"'") {
			t.Errorf("workers/static-site/src/headers.js lacks %s", link)
		}
	}
	if n := strings.Count(string(js), "; rel="); n != strings.Count(AgentLinks, "; rel=") {
		t.Errorf("the Worker sends %d links, the server %d", n, strings.Count(AgentLinks, "; rel="))
	}
}

func a2aCall(t *testing.T, s *Server, body string) (int, rpcResponse) {
	t.Helper()
	req := httptest.NewRequest("POST", "/a2a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var out rpcResponse
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("POST /a2a %s: %d %q", body, rec.Code, rec.Body.String())
		}
	}
	return rec.Code, out
}

func TestA2ARefusals(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec := agentGet(t, s, "/a2a"); rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Body.String(), "agent-card.json") {
		t.Errorf("GET /a2a: %d %s", rec.Code, rec.Body.String())
	}
	for body, code := range map[string]int{
		`{`: rpcParseError,
		`{"jsonrpc":"1.0","id":1,"method":"message/send"}`:                                                                             rpcInvalidRequest,
		`{"jsonrpc":"2.0","id":1,"method":"tasks/get"}`:                                                                                rpcMethodNotFound,
		`{"jsonrpc":"2.0","id":1,"method":"message/send","params":{"message":{"parts":[{"text":"  "}]}}}`:                              rpcInvalidParams,
		`{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"parts":[{"text":"` + strings.Repeat("ق", 200) + `"}]}}}`: rpcInvalidParams,
	} {
		status, out := a2aCall(t, s, body)
		if status != 200 || out.Error == nil || out.Error.Code != code {
			t.Errorf("%.60s: %d %+v, want error %d", body, status, out.Error, code)
		}
	}
	if status, _ := a2aCall(t, s, `{"jsonrpc":"2.0","method":"message/send"}`); status != http.StatusAccepted {
		t.Errorf("a notification: %d, want 202", status)
	}
}

func TestA2AText(t *testing.T) {
	en, no := "Labor Law", "136"
	got := a2aText("labour law", apiSearchResult{
		Laws:     []apiLawSummary{{ID: 190374, Kind: "قانون", Title: "قانون العمل", TitleEn: &en, Status: "in_force", URL: Base + "/law/190374/"}},
		LawCount: 12,
		Articles: []apiArticleHit{{Law: apiLawSummary{Kind: "مرسوم", Number: &no, Title: "طوارىء العمل", Status: "repealed"}, Number: "50", Snippet: "يحق  لكل\nمن", URL: Base + "/law/190374/article/50/"}},
		ArtCount: 3, SearchedInstead: "قانون العمل"})
	for _, want := range []string{`matching "labour law"`, `results for "قانون العمل"`, "Laws, decrees and decisions (12 found)",
		"1. قانون العمل (Labor Law, unofficial English title) · قانون · in force\n   " + Base + "/law/190374/",
		"1. Article 50, طوارىء العمل · مرسوم No. 136 · repealed\n   " + Base + "/law/190374/article/50/\n   يحق لكل من\n", notLegalAdvice} {
		if !strings.Contains(got, want) {
			t.Errorf("reply lacks %q:\n%s", want, got)
		}
	}
	if none := a2aText("zzz", apiSearchResult{}); !strings.Contains(none, `No Lebanese text matches "zzz"`) || !strings.Contains(none, notLegalAdvice) {
		t.Errorf("empty reply: %s", none)
	}
}

// With the corpus: a question gets a reply that links real texts, in both
// versions of the protocol.
func TestA2ASearch(t *testing.T) {
	s := testServer(t)
	for method, role := range map[string]string{"message/send": "agent", "SendMessage": "ROLE_AGENT"} {
		status, out := a2aCall(t, s, `{"jsonrpc":"2.0","id":"q1","method":"`+method+`","params":{"message":{"role":"user","contextId":"c1","parts":[{"kind":"text","text":"قانون العمل"}]}}}`)
		if status != 200 || out.Error != nil {
			t.Fatalf("%s: %d %+v", method, status, out.Error)
		}
		b, _ := json.Marshal(out.Result)
		var msg struct {
			Kind, Role, ContextID string
			Parts                 []struct {
				Text string
				Data *apiSearchResult
			}
			Message *json.RawMessage
		}
		if method == "SendMessage" {
			var wrap struct{ Message json.RawMessage }
			_ = json.Unmarshal(b, &wrap)
			b = wrap.Message
		}
		if err := json.Unmarshal(b, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Role != role || msg.ContextID != "c1" || len(msg.Parts) != 2 {
			t.Fatalf("%s: %s", method, b)
		}
		if !strings.Contains(msg.Parts[0].Text, Base+"/law/") || msg.Parts[1].Data == nil || msg.Parts[1].Data.LawCount == 0 {
			t.Errorf("%s: no law in the reply: %s", method, b)
		}
	}
}
