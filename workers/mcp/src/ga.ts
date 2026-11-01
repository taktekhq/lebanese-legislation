// Google Analytics 4 from the Worker, through the Measurement Protocol:
// MCP clients run no JavaScript and the Hugging Face dataset card can't
// load gtag, so the Worker sends the events itself, after the response
// (ctx.waitUntil), and a failed send never touches the answer.
//
// Each source has its own data stream in the site's GA4 property, so MCP
// traffic, dataset-card views and the site never mix: a stream is a
// measurement ID (wrangler.jsonc vars) plus that stream's Measurement
// Protocol API secret (a Worker secret, from GA Admin > Data streams >
// Measurement Protocol API secrets). A stream missing either sends
// nothing, so tests, `wrangler dev` and forks send nothing.
//
// No cookies and no stored IP: client_id is a hash of IP + User-Agent +
// the UTC day, so one visitor counts as one user per day and can't be
// followed across days. Location comes from Cloudflare's own request.cf,
// not the IP.

const GA_ENDPOINT = "https://www.google-analytics.com/mp/collect";
const MAX_VALUE = 100; // GA4's limit on an event parameter value (standard properties)

export type GAParams = Record<string, string | number | undefined>;

export interface GAStream {
  measurementId?: string;
  apiSecret?: string;
}

export function mcpStream(env: Env): GAStream {
  return { measurementId: env.GA_MCP_MEASUREMENT_ID, apiSecret: env.GA_MCP_API_SECRET };
}

export function hfStream(env: Env): GAStream {
  return { measurementId: env.GA_HF_MEASUREMENT_ID, apiSecret: env.GA_HF_API_SECRET };
}

export interface GAEvent {
  name: string;
  params: GAParams;
}

interface CfProperties {
  country?: string;
  city?: string;
  regionCode?: string;
}

// clip trims a parameter value to GA4's limit; GA drops longer ones whole.
export function clip(v: string, max = MAX_VALUE): string {
  return v.length > max ? v.slice(0, max) : v;
}

async function sha256Hex(s: string): Promise<string> {
  const buf = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(s));
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

// visitorIds derives the per-day client_id ("123.456", gtag's shape) and
// a numeric session_id from IP + User-Agent + day.
export async function visitorIds(ip: string, ua: string, day: string): Promise<{ clientId: string; sessionId: string }> {
  const h = await sha256Hex(`${ip}|${ua}|${day}`);
  const a = parseInt(h.slice(0, 8), 16);
  const b = parseInt(h.slice(8, 16), 16);
  const s = parseInt(h.slice(16, 24), 16);
  return { clientId: `${a}.${b}`, sessionId: String(s) };
}

// gaPayload builds the Measurement Protocol body. Every event carries the
// session and a token engagement time, without which GA4 counts no active
// users or sessions for Measurement Protocol traffic.
export function gaPayload(clientId: string, sessionId: string, cf: CfProperties | undefined, events: GAEvent[]): Record<string, unknown> {
  const body: Record<string, unknown> = {
    client_id: clientId,
    events: events.map((e) => {
      const params: Record<string, string | number> = { session_id: sessionId, engagement_time_msec: 1 };
      for (const [k, v] of Object.entries(e.params)) {
        if (v === undefined || v === "") continue;
        params[k] = typeof v === "string" ? clip(v) : v;
      }
      return { name: e.name, params };
    }),
  };
  if (cf?.country && /^[A-Z]{2}$/.test(cf.country)) {
    const loc: Record<string, string> = { country_id: cf.country };
    if (cf.city) loc.city = cf.city;
    if (cf.regionCode) loc.region_id = `${cf.country}-${cf.regionCode}`;
    body.user_location = loc;
  }
  return body;
}

// track sends events for this request to `stream` in the background. Safe
// to call with the stream unset or without an ExecutionContext (tests): it
// does nothing.
export function track(stream: GAStream, ctx: ExecutionContext | undefined, request: Request, events: GAEvent[]): void {
  if (!ctx || !stream.measurementId || !stream.apiSecret || events.length === 0) return;
  ctx.waitUntil(send(stream, request, events).catch((e) => console.error("ga:", e)));
}

async function send(stream: GAStream, request: Request, events: GAEvent[]): Promise<void> {
  const ip = request.headers.get("CF-Connecting-IP") ?? "";
  const ua = request.headers.get("User-Agent") ?? "";
  const day = new Date().toISOString().slice(0, 10);
  const { clientId, sessionId } = await visitorIds(ip, ua, day);
  const cf = (request as unknown as { cf?: CfProperties }).cf;
  const url = `${GA_ENDPOINT}?measurement_id=${encodeURIComponent(stream.measurementId!)}&api_secret=${encodeURIComponent(stream.apiSecret!)}`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(gaPayload(clientId, sessionId, cf, events)),
  });
  if (!res.ok) console.error("ga: measurement protocol answered", res.status);
}
