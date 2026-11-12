package site

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/taktekhq/lebanese-legislation/internal/mcpserver"
)

// What an AI agent reads to find this site's tools without being told where
// they are. Every file names things that exist and answer: the JSON API
// (api.go), the MCP server (internal/mcpserver) and the A2A lookup (a2a.go).
//
//	/.well-known/api-catalog               the APIs and their descriptions (RFC 9727)
//	/.well-known/mcp/server-card.json      the MCP server: address, transport, tools
//	/.well-known/agent-card.json           the A2A lookup at /a2a
//	/.well-known/agent-skills/index.json   one skill: how to find, read and cite a text
//	/.well-known/ai-catalog.json           all of the above in one list (ARD)
//	/.well-known/poppy.json                who runs the site (Personal Agent Protocol)
//	/webmcp.js (static/)                   search and article lookup as browser tools (WebMCP)
//	Link headers on every page             where the catalog, the API description and llms.txt are
//	robots.txt                             Content Signals: search, AI answers and training are all welcome
//
// Nothing here needs a sign-in, so no OAuth metadata is published: the
// /.well-known/oauth-* addresses stay a short 404 (probe404). MCP clients ask
// for them before connecting, and metadata there would send them into a
// sign-in that does not exist.
//
// Checked with https://isitagentready.com (POST /api/scan).

// agentsVersion is the version the discovery files carry; raise it when what
// they describe changes.
const agentsVersion = "1.0.0"

// AgentLinks is the Link header of every HTML page (RFC 8288). The static
// site's Worker sends the same value for pages it serves from R2
// (workers/static-site/src/headers.js); TestAgentLinksMatchTheWorker keeps
// them equal.
const AgentLinks = `</.well-known/api-catalog>; rel="api-catalog", ` +
	`</api/v1/openapi.json>; rel="service-desc"; type="application/vnd.oai.openapi+json", ` +
	`</developers/>; rel="service-doc"; type="text/html", ` +
	`</llms.txt>; rel="describedby"; type="text/markdown", ` +
	`</.well-known/mcp/server-card.json>; rel="https://modelcontextprotocol.io/server-card"; type="application/json", ` +
	`</sitemap.xml>; rel="sitemap"; type="application/xml"`

// contentSignal says what the pages may be used for (contentsignals.org):
// search results, answers written from them, and training. The texts are
// public law and the site exists so people and their assistants find them.
const contentSignal = "Content-Signal: search=yes, ai-input=yes, ai-train=yes\n"

const (
	agentsName    = "Lebanese Legislation"
	agentsSummary = "Every Lebanese law, decree, court ruling and Official Gazette issue, as published by the Lebanese University's Centre for Research and Studies in Legal Informatics. " +
		"Search, read one article's exact wording, follow amendments and citations. Texts are returned word for word, in Arabic, with the French where it was published. No key, no sign-in. Not legal advice."
)

// The skill file. {SITE} is filled in.
//
//go:embed agentskill.md.tmpl
var agentSkillTemplate string

// agentFile is one discovery file, built once.
type agentFile struct {
	contentType string
	body        []byte
}

func agentJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		panic("agent discovery file: " + err.Error())
	}
	return b.Bytes()
}

type jo = map[string]any

// agentFiles are the discovery files by path.
var agentFiles = func() map[string]agentFile {
	host := strings.TrimPrefix(Base, "https://")
	mcp, a2a := Base+"/mcp", Base+"/a2a"
	card, agentCard := Base+"/.well-known/mcp/server-card.json", Base+"/.well-known/agent-card.json"
	openapi := Base + "/api/v1/openapi.json"
	skillPath := "/.well-known/agent-skills/lebanese-legislation/SKILL.md"
	skill := strings.ReplaceAll(agentSkillTemplate, "{SITE}", Base)
	digest := sha256.Sum256([]byte(skill))

	var defs []struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(mcpserver.Tools(), &defs); err != nil {
		panic("agent discovery: the MCP tools: " + err.Error())
	}
	tools := make([]jo, len(defs))
	for i, d := range defs {
		tools[i] = jo{"name": d.Name, "title": d.Title}
	}

	link := func(href, typ string) []jo { return []jo{{"href": href, "type": typ}} }
	queries := []string{"what does Lebanese law say about dismissal notice", "المادة 50 من قانون العمل", "Lebanese Code of Obligations and Contracts article 122", "قانون الايجارات"}

	js := "application/json; charset=utf-8"
	return map[string]agentFile{
		"/.well-known/api-catalog": {`application/linkset+json; profile="https://www.rfc-editor.org/info/rfc9727"`, agentJSON(jo{"linkset": []jo{
			{"anchor": Base + "/api/v1/", "service-desc": link(openapi, "application/vnd.oai.openapi+json"), "service-doc": link(Base+"/developers/", "text/html"), "status": []jo{{"href": Base + "/healthz"}}},
			{"anchor": mcp, "service-desc": link(card, "application/json"), "service-doc": link(Base+"/ai/", "text/html")},
			{"anchor": a2a, "service-desc": link(agentCard, "application/json"), "service-doc": link(Base+"/ai/", "text/html")},
		}})},
		"/.well-known/mcp/server-card.json": {js, agentJSON(jo{
			"$schema": "https://static.modelcontextprotocol.io/schemas/mcp-server-card/v1.json", "name": "com.lebaneselegislation/mcp",
			"serverInfo": jo{"name": mcpserver.ServerName, "title": agentsName, "version": mcpserver.ServerVersion}, "version": mcpserver.ServerVersion,
			"description": agentsSummary, "url": mcp, "endpoint": mcp, "transport": jo{"type": "streamable-http", "endpoint": mcp},
			"remotes":      []jo{{"type": "streamable-http", "url": mcp}},
			"capabilities": jo{"tools": true, "resources": false, "prompts": false}, "authentication": jo{"required": false},
			"tools": tools, "documentation": Base + "/ai/", "repository": "https://github.com/taktekhq/lebanese-legislation"})},
		"/.well-known/agent-card.json": {js, agentJSON(jo{
			"name": agentsName + " lookup", "version": agentsVersion, "protocolVersion": "1.0",
			"description": "Send a question or a few words about Lebanese law; the reply lists the matching laws, articles, court rulings and Gazette issues with their links, and carries the same results as data. " +
				"A search over the published texts with no language model behind it: it quotes and links, it does not interpret. " + notLegalAdvice,
			"url": a2a, "preferredTransport": "JSONRPC",
			"supportedInterfaces": []jo{{"url": a2a, "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}},
			"provider":            jo{"organization": "Taktek, LLC", "url": "https://taktek.io/"}, "documentationUrl": Base + "/ai/",
			"capabilities": jo{"streaming": false, "pushNotifications": false}, "defaultInputModes": []string{"text/plain"}, "defaultOutputModes": []string{"text/plain", "application/json"},
			"skills": []jo{{"id": "find-lebanese-law", "name": "Find Lebanese law", "tags": []string{"law", "lebanon", "legislation", "rulings", "official gazette"},
				"description": "Finds Lebanese laws, decrees, articles, court rulings and Official Gazette issues that match a question or a citation, in Arabic, English or French, and returns titles, dates, status and links.",
				"examples":    queries}}})},
		skillPath: {"text/markdown; charset=utf-8", []byte(skill)},
		"/.well-known/agent-skills/index.json": {js, agentJSON(jo{"$schema": "https://schemas.agentskills.io/discovery/0.2.0/schema.json", "skills": []jo{{
			"name": "lebanese-legislation", "type": "skill-md", "description": "Find, read and cite Lebanese laws, articles, court rulings and Official Gazette issues, word for word, from LebaneseLegislation.com.",
			"url": skillPath, "digest": "sha256:" + hex.EncodeToString(digest[:])}}})},
		"/.well-known/ai-catalog.json": {js, agentJSON(jo{"specVersion": "1.0",
			"host": jo{"displayName": agentsName, "identifier": "did:web:" + host, "documentationUrl": Base + "/llms.txt"},
			"entries": []jo{
				{"identifier": "urn:air:" + host + ":server:mcp", "displayName": agentsName + " (MCP server)", "description": agentsSummary, "type": "application/mcp-server-card+json", "url": card, "representativeQueries": queries},
				{"identifier": "urn:air:" + host + ":agent:lookup", "displayName": agentsName + " lookup (A2A)", "description": "Replies to a question with the matching laws, articles, rulings and Gazette issues and their links. A search, not a language model.", "type": "application/a2a-agent-card+json", "url": agentCard, "representativeQueries": queries[:2]},
				{"identifier": "urn:air:" + host + ":api:v1", "displayName": agentsName + " JSON API (OpenAPI)", "description": "Search, laws, articles, rulings and Gazette issues as JSON, read-only.", "type": "application/openapi+json", "url": openapi, "representativeQueries": queries[:2]},
				{"identifier": "urn:air:" + host + ":skill:lebanese-legislation", "displayName": "How to find, read and cite Lebanese law", "description": "A skill for agents.", "type": "text/markdown", "url": Base + skillPath, "representativeQueries": queries[:2]},
			}})},
		// Website only: declaring the API or the MCP server here would need an
		// OAuth issuer (draft 0.1, section 3), and there is none.
		"/.well-known/poppy.json": {js, agentJSON(jo{"protocol_version": "0.1", "organization": jo{"name": agentsName, "domain": host}, "web": jo{}})},
	}
}()

// agentRoutes serves the discovery files. They are the same for every
// caller and read nothing, so they are kept a day like the 404 beside them.
func (s *Server) agentRoutes() {
	for path, f := range agentFiles {
		s.mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", f.contentType)
			w.Header().Set("Cache-Control", "public, max-age=3600, s-maxage=86400")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			_, _ = w.Write(f.body)
		})
	}
}
