import { SECURITY_HEADERS } from "./headers.js";

const liveSides = new WeakMap();
async function liveSide(env) {
  const cached = liveSides.get(env.SITE);
  if (cached && Date.now() - cached.at < 15_000) return cached.side;
  const obj = await env.SITE.get("_build/live-side");
  const side = obj && (await obj.text()).trim() === "green" ? "green" : "blue";
  liveSides.set(env.SITE, { side, at: Date.now() });
  return side;
}

function originService(env, side) {
  return side === "green" ? env.ORIGIN_GREEN : env.ORIGIN_BLUE;
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
  const admin = url.pathname === "/_origin/log" || url.pathname === "/_origin/restart";
  const selected = testing ? (admin ? url.searchParams.get("side") : request.headers.get("x-origin-side")) : null;
  if (selected !== null && selected !== "blue" && selected !== "green") {
    return new Response("invalid origin side\n", { status: 400, headers: SECURITY_HEADERS });
  }
  // Only authenticated requests may invoke the service's container controls.
  if (admin && !testing) return new Response(null, { status: 404, headers: SECURITY_HEADERS });
  // Until the container's AI answer works, AI_TO_HETZNER sends the AI answer
  // endpoints to the old origin. Remove it once Hetzner is retired.
  const aiPath = /^\/(en\/|ar\/)?search\/(answer|ai)(\/|$)/.test(url.pathname);
  if ((env.ORIGIN_MODE === "hetzner" || (env.AI_TO_HETZNER === "1" && aiPath)) && !testing) return fetch(request);
  try {
    const side = selected || await liveSide(env);
    if (testing && admin) {
      const res = await originService(env, side).fetch(request);
      const headers = new Headers(res.headers);
      for (const [key, value] of Object.entries(SECURITY_HEADERS)) headers.set(key, value);
      return new Response(res.body, { status: res.status, headers });
    }
    // API answers are public and say how long they keep (Cache-Control), so
    // repeats are served from the edge and never reach Postgres.
    const cacheable = !testing && request.method === "GET" && /^\/(en\/)?api\/v1\//.test(url.pathname);
    const cacheURL = new URL(url);
    cacheURL.searchParams.set("__origin_side", side);
    const cacheKey = new Request(cacheURL.toString(), { method: "GET" });
    let res = cacheable ? await caches.default.match(cacheKey) : undefined;
    if (!res) {
      res = await originService(env, side).fetch(request);
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
