// Counts requests that ask for Markdown (Accept: text/markdown), to decide
// whether serving Markdown versions of the pages is worth building (Nizar,
// 10 Nov 2026: "No need to implement it if people barely ask for it.").
// One PostHog event per such request, sent after the response; requests that
// do not ask for Markdown send nothing, so read time is unchanged.

import { isTestRequest, requestRoute } from "./origin.js";

// Who is asking, by User-Agent: [name, pattern, family, operator, purpose].
// Purpose is what the fetch is for: "user-fetch" (a person's assistant reads
// the page now), "search-index" (the company's search index) or "training".
// Order matters: the assistants that fetch for a person come before their
// companies' crawlers, and named agents before the catch-all rows.
const AGENTS = [
  ["ChatGPT-User", /ChatGPT-User/i, "chatgpt-user", "openai", "user-fetch"],
  ["OAI-SearchBot", /OAI-SearchBot/i, "openai-crawler", "openai", "search-index"],
  ["GPTBot", /GPTBot/i, "openai-crawler", "openai", "training"],
  ["Claude-User", /Claude-User/i, "claude-user", "anthropic", "user-fetch"],
  ["claude-code", /claude-code/i, "claude-code", "anthropic", "user-fetch"],
  ["Claude-SearchBot", /Claude-SearchBot/i, "anthropic-crawler", "anthropic", "search-index"],
  ["ClaudeBot", /ClaudeBot/i, "anthropic-crawler", "anthropic", "training"],
  ["anthropic-ai", /anthropic-ai/i, "anthropic-crawler", "anthropic", "training"],
  ["Perplexity-User", /Perplexity-User/i, "perplexity", "perplexity", "user-fetch"],
  ["PerplexityBot", /Perplexity/i, "perplexity", "perplexity", "search-index"],
  ["ExaUser", /ExaUser/i, "exa", "exa", "user-fetch"],
  ["ExaSearchBot", /ExaSearchBot|ExaBot/i, "exa", "exa", "search-index"],
  ["MistralAI-User", /MistralAI-User/i, "mistral", "mistral", "user-fetch"],
  ["DuckAssistBot", /DuckAssistBot/i, "duckduckgo", "duckduckgo", "user-fetch"],
  ["Google-Extended", /Google-Extended/i, "google", "google", "training"],
  ["Googlebot", /Googlebot|GoogleOther|Google-InspectionTool/i, "google", "google", "search-index"],
  ["Google", /Google|Gemini/i, "google", "google", ""],
  ["bingbot", /bingbot|BingPreview/i, "bing", "microsoft", "search-index"],
  ["meta-externalfetcher", /meta-externalfetcher|facebookexternalhit/i, "meta", "meta", "user-fetch"],
  ["meta-externalagent", /meta-external/i, "meta", "meta", "training"],
  ["Applebot-Extended", /Applebot-Extended/i, "apple", "apple", "training"],
  ["Applebot", /Applebot/i, "apple", "apple", "search-index"],
  ["Amazonbot", /Amazonbot/i, "amazon", "amazon", "search-index"],
  ["Bytespider", /Bytespider/i, "bytedance", "bytedance", "training"],
  ["CCBot", /CCBot/i, "commoncrawl", "commoncrawl", "training"],
  ["", /cursor|windsurf|codex|copilot|cline|zed|goose|aider/i, "coding-tool", "", "user-fetch"],
  ["", /^(node|undici|curl|wget|python|go-http-client|okhttp|axios|java|ruby|php|deno|bun)\b|aiohttp|httpx|requests\//i, "library", "", ""],
];

// A name for an agent that is not in the list: the first product token that
// calls itself a bot, crawler, spider or fetcher, else the first token of a
// User-Agent that is not a browser's.
const BOT_TOKEN = /([A-Za-z][\w.-]*(?:bot|crawler|spider|fetcher|scraper)[\w-]*)(?:\/(\d[\w.]*))?/i;
const FIRST_TOKEN = /^([A-Za-z][\w.-]*)(?:\/(\d[\w.]*))?/;

// agentInfo says who is asking: family (the coarse group), name and version
// (the exact agent, "ExaSearchBot" "1.0"), operator (the company), purpose,
// and client (the tool a Claude-User fetch was made from, "claude-code").
export function agentInfo(ua) {
  ua = String(ua || "");
  const info = { family: "other", name: "", version: "", operator: "", purpose: "", client: "", client_version: "" };
  const row = AGENTS.find(([, re]) => re.test(ua));
  if (row) [info.name, , info.family, info.operator, info.purpose] = row;
  if (info.name) {
    const m = new RegExp(info.name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\/(\\d[\\w.]*)", "i").exec(ua);
    if (m) info.version = m[1];
  } else {
    const m = BOT_TOKEN.exec(ua) || (/^Mozilla\//i.test(ua) ? null : FIRST_TOKEN.exec(ua));
    if (m) { info.name = m[1]; info.version = m[2] || ""; }
  }
  const c = /(claude-code)\/(\d[\w.]*)/i.exec(ua);
  if (c && info.name !== "claude-code") { info.client = "claude-code"; info.client_version = c[2]; }
  for (const k of ["name", "version", "client_version"]) info[k] = info[k].slice(0, 60);
  return info;
}

export function agentFamily(ua) {
  return agentInfo(ua).family;
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
    const who = agentInfo(ua);
    ctx.waitUntil(Promise.resolve().then(() => fetch("https://eu.i.posthog.com/i/v0/e/", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        api_key: env.POSTHOG_KEY, event: "markdown_requested", distinct_id: "lebleg-markdown-ask",
        properties: {
          agent: who.family, agent_name: who.name, agent_version: who.version, operator: who.operator,
          purpose: who.purpose, client: who.client, client_version: who.client_version,
          user_agent: ua.slice(0, 200), accept: accept.slice(0, 200),
          markdown_first: markdownFirst(accept), route: requestRoute(url), method: request.method,
          // The network the request came from, to tell a real agent from a
          // copied User-Agent. No IP address is stored.
          asn: request.cf?.asn, as_org: String(request.cf?.asOrganization || "").slice(0, 80),
          verified_bot_category: request.cf?.verifiedBotCategory || "", country: request.cf?.country,
          colo: request.cf?.colo, $lib: "lebleg-static-site-worker", $process_person_profile: false,
        },
      }),
    })).catch(() => {}));
  } catch {}
}
