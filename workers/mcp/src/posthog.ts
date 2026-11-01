// PostHog from the Worker, next to GA4 (src/ga.ts): MCP calls, dataset-card
// views and the daily Hugging Face stats go to the LebaneseLegislation
// project through PostHog's /batch/ capture endpoint. Built like ga.ts: the
// send runs after the response (ctx.waitUntil), a failed send never touches
// the answer, and with POSTHOG_KEY unset (tests, `wrangler dev`, forks)
// nothing is sent.
//
// Unlike GA4, PostHog keeps everything (Nizar, 20 Oct 2026): the visitor's
// IP (PostHog resolves the location from it) and a stable id per machine,
// an HMAC of IP + User-Agent with a Worker secret, so one machine is one
// person across days and across the MCP and the badge, and the id can't be
// reversed into an IP.

const DEFAULT_HOST = "https://eu.i.posthog.com";
const LIB = "lebaneselegislation-worker";
const MAX_STRING = 10_000; // keeps an oversized argument from getting the event dropped

export interface PHEvent {
  event: string;
  properties: Record<string, unknown>;
  distinctId?: string;
  timestamp?: string;
}

// clipProps trims every string (at any depth) to `max` characters.
export function clipProps(p: Record<string, unknown>, max = MAX_STRING): Record<string, unknown> {
  const clipValue = (v: unknown): unknown => {
    if (typeof v === "string") return v.length > max ? v.slice(0, max) : v;
    if (Array.isArray(v)) return v.map(clipValue);
    if (v && typeof v === "object") return clipProps(v as Record<string, unknown>, max);
    return v;
  };
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(p)) out[k] = clipValue(v);
  return out;
}

function hex(buf: ArrayBuffer): string {
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

// visitorId is HMAC-SHA256(POSTHOG_ID_SALT, ip|ua), or plain SHA-256 when
// the salt is unset (tests, forks).
export async function visitorId(env: Env, ip: string, ua: string): Promise<string> {
  const data = new TextEncoder().encode(`${ip}|${ua}`);
  if (!env.POSTHOG_ID_SALT) return hex(await crypto.subtle.digest("SHA-256", data));
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(env.POSTHOG_ID_SALT), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return hex(await crypto.subtle.sign("HMAC", key, data));
}

// phCapture sends `events` in the background. `request` supplies the IP and
// User-Agent; without one (the cron), each event must carry its distinctId.
export function phCapture(env: Env, ctx: ExecutionContext | undefined, request: Request | undefined, events: PHEvent[]): void {
  if (!ctx || !env.POSTHOG_KEY || events.length === 0) return;
  ctx.waitUntil(send(env, request, events).catch((e) => console.error("posthog:", e)));
}

async function send(env: Env, request: Request | undefined, events: PHEvent[]): Promise<void> {
  const ip = request?.headers.get("CF-Connecting-IP") ?? "";
  const ua = request?.headers.get("User-Agent") ?? "";
  const id = request ? await visitorId(env, ip, ua) : "lebaneselegislation-worker";
  const batch = events.map((e) => ({
    event: e.event,
    timestamp: e.timestamp ?? new Date().toISOString(),
    properties: {
      ...clipProps(e.properties),
      distinct_id: e.distinctId ?? id,
      $lib: LIB,
      ...(ip ? { $ip: ip } : {}),
    },
  }));
  const res = await fetch(`${env.POSTHOG_HOST ?? DEFAULT_HOST}/batch/`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ api_key: env.POSTHOG_KEY, batch }),
  });
  if (!res.ok) console.error("posthog: capture answered", res.status);
}
