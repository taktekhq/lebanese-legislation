package mcpserver

import (
	"regexp"
	"strings"
)

// Who is calling the MCP: a person's assistant, a directory that lists the
// server, a liveness monitor, our own tools, a bare HTTP library, or something
// else. Most /mcp traffic is bots that check the server is alive or list it in
// a directory: they connect and list tools, and never ask a question. PostHog
// keeps them, tagged, so the dashboard can show real use apart from bots,
// directories and our own traffic (9 Nov 2026: our checks and our coding
// agents' sessions had been counted as real use).

var (
	// User-Agents of the assistants that call on a person's behalf (OpenAI's
	// MCP client serves ChatGPT apps and the Responses API; Claude Code and
	// Codex are a developer's assistant unless they carry internalHeader), and the
	// clientInfo names that desktop and IDE clients send on initialize.
	assistantUA     = regexp.MustCompile(`(?i)^(Claude-User|ChatGPT-User|Perplexity-User|MistralAI-User|Gemini-User|Copilot|openai-mcp/|claude-code/|codex-mcp-client)|^Google$`)
	assistantClient = regexp.MustCompile(`(?i)claude|chatgpt|openai|cursor|windsurf|vscode|copilot|cline|continue|zed|goose|librechat|perplexity|mistral|gemini`)
	// Our own tools: livecheck, deploy, parity and latency checks, scripts.
	ownUA = regexp.MustCompile(`(?i)^(livecheck|lebleg|lebaneselegislation|taktekbot)`)
	// Directories and indexes that list MCP servers. They bring people to us,
	// so they get their own kind instead of hiding among monitors.
	registryUA = regexp.MustCompile(`(?i)registry|directory|catalog|\bindex\b|-index\b|toolindex|badges|allagents|mcptry|glama|smithery|pulsemcp`)
	// Liveness, research and probe bots describe themselves.
	monitorUA = regexp.MustCompile(`(?i)bot\b|bot/|probe|liveness|monitor|collector|sentinel|harvest|scan|crawl|spider|watch|research|spike|health|survey|tlog|beat\b|beat/|rugpull|uptime|bench|vet\b|vetprobe|check/|-check\b|checks\b|mcp check`)
	// Bare HTTP stacks: someone's script, could be a person or a bot.
	libraryUA = regexp.MustCompile(`(?i)^(node|undici|curl|wget|python|go-http-client|okhttp|axios|java|ruby|php|dart|deno|bun)\b|aiohttp|httpx|requests/`)
)

// internalHeader marks our own sessions: the repo's .mcp.json sends it, so a
// Claude Code session opened in this repo counts as ours, not a user.
const internalHeader = "X-Taktek-Internal"

// clientKind is "assistant", "internal", "registry", "monitor", "library" or
// "other". internal is true when the request carried internalHeader.
func clientKind(ua, clientName string, internal bool) string {
	switch {
	case internal || ownUA.MatchString(ua):
		return "internal"
	case assistantUA.MatchString(ua):
		return "assistant"
	case registryUA.MatchString(ua) || clientName != "" && registryUA.MatchString(clientName):
		return "registry"
	case monitorUA.MatchString(ua):
		return "monitor"
	case clientName != "" && monitorUA.MatchString(clientName):
		return "monitor"
	case clientName != "" && assistantClient.MatchString(clientName):
		return "assistant"
	case strings.TrimFunc(ua, isJSSpace) == "" || libraryUA.MatchString(ua):
		return "library"
	}
	return "other"
}

// notPerson says a kind is never a person: PostHog keeps no profile for it.
func notPerson(kind string) bool {
	return kind == "monitor" || kind == "registry" || kind == "internal"
}
