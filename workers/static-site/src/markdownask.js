// Counts requests that ask for Markdown (Accept: text/markdown), to decide
// whether serving Markdown versions of the pages is worth building (Nizar,
// 10 Nov 2026: "No need to implement it if people barely ask for it.").
// One PostHog event per such request, sent after the response; requests that
// do not ask for Markdown send nothing, so read time is unchanged.

import { isTestRequest, requestRoute } from "./origin.js";

// Who is asking, by User-Agent, in a few families. Order matters: the
// assistants that fetch for a person come before their companies' crawlers.
const FAMILIES = [
  ["chatgpt-user", /ChatGPT-User/i],
  ["openai-crawler", /OAI-SearchBot|GPTBot/i],
  ["claude-user", /Claude-User/i],
  ["claude-code", /claude-code|Claude-Code/i],
  ["anthropic-crawler", /ClaudeBot|Claude-SearchBot|anthropic-ai/i],
  ["perplexity", /Perplexity/i],
  ["google", /Google|Gemini/i],
  ["bing", /bingbot|BingPreview/i],
  ["meta", /meta-external|facebookexternalhit/i],
  ["apple", /Applebot/i],
  ["coding-tool", /cursor|windsurf|codex|copilot|cline|zed|goose|aider/i],
  ["library", /^(node|undici|curl|wget|python|go-http-client|okhttp|axios|java|ruby|php|deno|bun)\b|aiohttp|httpx|requests\//i],
];

export function agentFamily(ua) {
  for (const [name, re] of FAMILIES) if (re.test(ua || "")) return name;
  return "other";
}

// asksMarkdown says whether the Accept header names Markdown at all.
export function asksMarkdown(accept) {
  return /text\/(x-)?markdown/i.test(accept || "");
}

// markdownFirst says whether Markdown is preferred over HTML: a higher q, or
// the same q and listed first (or HTML not listed).
export function markdownFirst(accept) {
  const prefs = [];
  for (const [i, part] of String(accept || "").split(",").entries()) {
    const [type, ...params] = part.trim().toLowerCase().split(";");
    let q = 1;
    for (const p of params) {
      const m = /^\s*q\s*=\s*([0-9.]+)\s*$/.exec(p);
      if (m) q = Number(m[1]);
    }
    prefs.push({ type: type.trim(), q, i });
  }
  const best = re => prefs.filter(p => re.test(p.type)).sort((a, b) => b.q - a.q || a.i - b.i)[0];
  const md = best(/^text\/(x-)?markdown$/), html = best(/^text\/html$|^\*\/\*$|^text\/\*$/);
  if (!md || md.q === 0) return false;
  if (!html) return true;
  return md.q > html.q || (md.q === html.q && md.i < html.i);
}

// A cap per Worker isolate, so one client looping cannot flood PostHog.
const CAP_PER_MINUTE = 120;
let windowStart = 0, sent = 0;

export function markdownAsk(request, env, ctx) {
  // Telemetry is best effort, including synchronous scheduling failures.
  try {
    if (!env.POSTHOG_KEY || !ctx?.waitUntil) return;
    const accept = request.headers.get("Accept") || "";
    if (!asksMarkdown(accept)) return;
    if (isTestRequest(request, env)) return;
    const now = Date.now();
    if (now - windowStart >= 60_000) { windowStart = now; sent = 0; }
    if (++sent > CAP_PER_MINUTE) return;
    const ua = request.headers.get("User-Agent") || "";
    const url = new URL(request.url);
    ctx.waitUntil(Promise.resolve().then(() => fetch("https://eu.i.posthog.com/i/v0/e/", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        api_key: env.POSTHOG_KEY, event: "markdown_requested", distinct_id: "lebleg-markdown-ask",
        properties: {
          agent: agentFamily(ua), user_agent: ua.slice(0, 200), accept: accept.slice(0, 200),
          markdown_first: markdownFirst(accept), route: requestRoute(url), method: request.method,
          colo: request.cf?.colo, $lib: "lebleg-static-site-worker", $process_person_profile: false,
        },
      }),
    })).catch(() => {}));
  } catch {}
}
