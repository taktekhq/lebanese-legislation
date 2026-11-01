import { Container, getContainer } from "@cloudflare/containers";
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
    if (!this.ctx.container?.running) {
      await this.startAndWaitForPorts({
        ports: [this.defaultPort],
        cancellationOptions: { instanceGetTimeoutMS: 120_000, portReadyTimeoutMS: 300_000 },
      });
    }
    return super.fetch(request);
  }
}

export async function originFetch(request, env) {
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
  if (env.ORIGIN_MODE === "hetzner" && !testing) return fetch(request);
  try {
    const res = await getContainer(env.ORIGIN, "main").fetch(request);
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
