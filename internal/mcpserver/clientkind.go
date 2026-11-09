package mcpserver

import (
	"regexp"
	"strings"
)

// Who is calling the MCP: a person's assistant, a monitoring or registry bot,
// a bare HTTP library, or something else. Most /mcp traffic is bots that
// check the server is alive or list it in a directory: they connect and list
// tools, and never ask a question. PostHog keeps them, tagged, so the
// dashboard can show real use.

var (
	// User-Agents of the assistants that fetch on a person's behalf, and the
	// clientInfo names that desktop and IDE clients send on initialize.
	assistantUA     = regexp.MustCompile(`(?i)^(Claude-User|ChatGPT-User|Perplexity-User|MistralAI-User|Gemini-User|Copilot)`)
	assistantClient = regexp.MustCompile(`(?i)claude|chatgpt|openai|cursor|windsurf|vscode|copilot|cline|continue|zed|goose|librechat|perplexity|mistral|gemini`)
	// Monitoring, registry, research and probe bots describe themselves, and so
	// do our own checks (livecheck, deploy and latency checks): counting them
	// as real use would make the "no monitoring bots" p95 look better than
	// what people get (9 Nov 2026).
	monitorUA = regexp.MustCompile(`(?i)bot\b|bot/|probe|liveness|monitor|collector|sentinel|harvest|scan|crawl|spider|watch|research|spike|health|survey|tlog|beat\b|beat/|rugpull|registry|uptime|bench|vet\b|vetprobe|livecheck|check/|-check\b`)
	// Bare HTTP stacks: someone's script, could be a person or a bot.
	libraryUA = regexp.MustCompile(`(?i)^(node|undici|curl|wget|python|go-http-client|okhttp|axios|java|ruby|php|dart|deno|bun)\b|aiohttp|httpx|requests/`)
)

// clientKind is "assistant", "monitor", "library" or "other".
func clientKind(ua, clientName string) string {
	switch {
	case assistantUA.MatchString(ua):
		return "assistant"
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
