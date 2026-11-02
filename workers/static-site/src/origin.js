import { Container } from "@cloudflare/containers";
import { SECURITY_HEADERS } from "./headers.js";

export class Origin extends Container {
  defaultPort = 8080;
  sleepAfter = "72h";

  constructor(ctx, env) {
    super(ctx, env);
    this.envVars = Object.fromEntries([
      "GEMINI_API_KEY", "POSTHOG_KEY", "LEBLEG_SEARCH_PROVIDER",
      "LEBLEG_VECTORIZE_INDEX", "CLOUDFLARE_ACCOUNT_ID", "LEBLEG_VECTORIZE_TOKEN",
      "LEBLEG_SEARCH_MODEL", "LEBLEG_SEARCH_SELECT_MODEL",
      "LEBLEG_SEARCH_DAILY_LIMIT", "LEBLEG_SEARCH_SHARES_DATA",
    ].filter((key) => env[key] !== undefined).map((key) => [key, env[key]]));
  }

  // A cold start opens a 10 GB database and warms the server's caches before it
  // listens, which takes longer than the library's default 20 seconds.
  async fetch(request) {
    // Also when the container is already running: it may still be opening
    // Postgres, and the library's own wait gives up after about 20 to 48 s.
    // Wait for the port once per start; checking it on every request added
    // about 1.5 s to each response.
    if (!this.portReady) {
      await this.startAndWaitForPorts({
        ports: [this.defaultPort],
        cancellationOptions: { instanceGetTimeoutMS: 120_000, portReadyTimeoutMS: 660_000 },
      });
      this.portReady = true;
    }
    try {
      return await super.fetch(request);
    } catch (err) {
      this.portReady = false;
      throw err;
    }
  }

  onStop() {
    this.portReady = false;
  }
}

export class OriginBlue extends Origin {}
export class OriginGreen extends Origin {}

// A Durable Object stays where it is first created; the first "main" landed in
// Dallas. "eu-1" is created with a Western Europe hint.
function originStub(env, side) {
  const namespace = side === "green" ? env.ORIGIN_GREEN : env.ORIGIN_BLUE;
  return namespace.get(namespace.idFromName("eu-1"), { locationHint: "weur" });
}

export async function originFetch(request, env, ctx) {
  const url = new URL(request.url);
  if (url.pathname.startsWith("/ph/")) {
    const host = url.pathname.startsWith("/ph/static/")
      ? "https://eu-assets.i.posthog.com" : "https://eu.i.posthog.com";
    return fetch(new Request(host + url.pathname.slice(3) + url.search, request));
  }

  const ruling = /^\/ruling-files\/([1-9][0-9]{0,8})\.pdf$/.exec(url.pathname);
  if (ruling) {
    const id = ruling[1];
    const shard = String(Math.floor(Number(id) / 1000)).padStart(4, "0");
    const obj = await env.SITE.get(`ruling-files/${shard}/${id}.pdf`);
    if (obj) {
      return new Response(request.method === "HEAD" ? null : obj.body, {
        status: 200,
        headers: {
          ...SECURITY_HEADERS,
          "Content-Type": "application/pdf",
          "Cache-Control": "public, max-age=86400",
          "X-Robots-Tag": "noindex",
        },
      });
    }
  }

  // A request with the secret test header reaches the container whatever the mode,
  // so the container can be checked before traffic moves to it.
  const testing = env.ORIGIN_TEST_KEY && request.headers.get("x-origin-test") === env.ORIGIN_TEST_KEY;
  const live = env.LIVE === "green" ? "green" : "blue";
  const admin = url.pathname === "/_origin/log" || url.pathname === "/_origin/restart";
  const selected = testing ? (admin ? url.searchParams.get("side") : request.headers.get("x-origin-side")) : null;
  if (selected !== null && selected !== "blue" && selected !== "green") {
    return new Response("invalid origin side\n", { status: 400, headers: SECURITY_HEADERS });
  }
  const side = selected || live;
  if (testing && url.pathname === "/_origin/log") {
    // The container's start log (Postgres and the server write to it), served on port 8081.
    return originStub(env, side).containerFetch("http://container/log", 8081);
  }
  if (testing && new URL(request.url).pathname === "/_origin/restart") {
    // Stops the container so the next request starts it with the current settings.
    await originStub(env, side).destroy();
    return new Response("container stopped\n", { headers: SECURITY_HEADERS });
  }
  // Until the container's AI answer works, AI_TO_HETZNER sends the AI answer
  // endpoints to the old origin. Remove it once Hetzner is retired.
  const aiPath = /^\/(en\/|ar\/)?search\/(answer|ai)(\/|$)/.test(url.pathname);
  if ((env.ORIGIN_MODE === "hetzner" || (env.AI_TO_HETZNER === "1" && aiPath)) && !testing) return fetch(request);
  try {
    // API answers are public and say how long they keep (Cache-Control), so
    // repeats are served from the edge and never reach Postgres.
    const cacheable = !testing && request.method === "GET" && /^\/(en\/)?api\/v1\//.test(url.pathname);
    const cacheURL = new URL(url);
    cacheURL.searchParams.set("__origin_side", side);
    const cacheKey = new Request(cacheURL.toString(), { method: "GET" });
    let res = cacheable ? await caches.default.match(cacheKey) : undefined;
    if (!res) {
      res = await originStub(env, side).fetch(request);
      if (cacheable && res.status === 200 && !/no-store|private/.test(res.headers.get("Cache-Control") || "")) {
        ctx?.waitUntil(caches.default.put(cacheKey, res.clone()));
      }
    }
    const headers = new Headers(res.headers);
    for (const [key, value] of Object.entries(SECURITY_HEADERS)) {
      if (!headers.has(key)) headers.set(key, value);
    }
    return new Response(res.body, { status: res.status, statusText: res.statusText, headers });
  } catch (err) {
    console.error("container origin:", err && err.message);
    return new Response(testing ? "container error: " + (err && err.stack || err) : "Service unavailable", {
      status: 503,
      headers: { ...SECURITY_HEADERS, "Retry-After": "30" },
    });
  }
}
