// Who is calling the MCP: a person's assistant, a monitoring or registry bot,
// a bare HTTP library, or something else. Most /mcp traffic is bots that
// check the server is alive or list it in a directory: they connect and list
// tools, and never ask a question (SentinelOracle alone sent 3,471 requests
// in the week to 20 Oct 2026). PostHog keeps them, tagged, so the dashboard
// can show real use.

export type ClientKind = "assistant" | "monitor" | "library" | "other";

// User-Agents of the assistants that fetch on a person's behalf, and the
// clientInfo names that desktop and IDE clients send on initialize.
const ASSISTANT_UA = /^(Claude-User|ChatGPT-User|Perplexity-User|MistralAI-User|Gemini-User|Copilot)/i;
const ASSISTANT_CLIENT = /claude|chatgpt|openai|cursor|windsurf|vscode|copilot|cline|continue|zed|goose|librechat|perplexity|mistral|gemini/i;

// Monitoring, registry, research and probe bots describe themselves.
const MONITOR = /bot\b|bot\/|probe|liveness|monitor|collector|sentinel|harvest|scan|crawl|spider|watch|research|spike|health|survey|tlog|beat\b|beat\/|rugpull|registry|uptime|bench|vet\b|vetprobe/i;

// Bare HTTP stacks: someone's script, could be a person or a bot.
const LIBRARY = /^(node|undici|curl|wget|python|go-http-client|okhttp|axios|java|ruby|php|dart|deno|bun)\b|aiohttp|httpx|requests\//i;

export function clientKind(ua: string, clientName: string | undefined): ClientKind {
  if (ASSISTANT_UA.test(ua)) return "assistant";
  if (MONITOR.test(ua)) return "monitor";
  if (clientName && MONITOR.test(clientName)) return "monitor";
  if (clientName && ASSISTANT_CLIENT.test(clientName)) return "assistant";
  if (ua.trim() === "" || LIBRARY.test(ua)) return "library";
  return "other";
}
