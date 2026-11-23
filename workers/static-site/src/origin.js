import { SECURITY_HEADERS } from "./headers.js";
import { gtagRelay } from "./gtagrelay.js";

// sameSecret compares a header with a secret in time that does not depend on
// where they first differ.
export function sameSecret(given, secret) {
  if (!secret || typeof given !== "string") return false;
  const enc = new TextEncoder();
  const a = enc.encode(given), b = enc.encode(secret);
  let diff = a.length ^ b.length;
  for (let i = 0; i < b.length; i++) diff |= (a[i] ?? 0) ^ b[i];
  return diff === 0;
}

// isTestRequest says whether a request carries the secret test header.
export function isTestRequest(request, env) {
  return sameSecret(request.headers.get("x-origin-test"), env.ORIGIN_TEST_KEY);
}

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

// requestRoute names which part of the site a request used: the API endpoint (/api/v1/search/), else the address's first
// word (/laws/, /law/, /ul/), with "?" when it carries a query. Never an id or
// a query's value: the event stays anonymous.
export function requestRoute(url) {
  const path = url.pathname.replace(/^\/(en|ar)(?=\/|$)/, "");
  const seg = path.split("/").filter(Boolean);
  const word = s => (/^[a-z][a-z0-9_.-]{0,30}$/i.test(s) ? s : "other");
  let route;
  if (seg[0] === "api" && seg[1] === "v1") {
    route = "/api/v1/" + (seg[2] ? word(seg[2]) + "/" : "");
  } else {
    route = seg[0] ? "/" + word(seg[0]) + (seg.length > 1 || path.endsWith("/") ? "/" : "") : "/";
  }
  return url.search.length > 1 ? route + "?" : route;
}

export async function originFetch(request, env, ctx) {
  const url = new URL(request.url);
  if (url.pathname.startsWith("/gtag/")) return gtagRelay(request, SECURITY_HEADERS);

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

  // A request with the secret test header can select the idle container
  // so it can be checked before traffic moves to it.
  const testing = isTestRequest(request, env);
  const admin = url.pathname === "/_origin/log" || url.pathname === "/_origin/restart";
  const selected = testing ? (admin ? url.searchParams.get("side") : request.headers.get("x-origin-side")) : null;
  if (selected !== null && selected !== "blue" && selected !== "green") {
    return new Response("invalid origin side\n", { status: 400, headers: SECURITY_HEADERS });
  }
  // Only authenticated requests may invoke the service's container controls.
  if (admin && !testing) return new Response(null, { status: 404, headers: SECURITY_HEADERS });
  let side;
  try {
    side = selected || await liveSide(env);
    if (testing && admin) {
      const res = await originService(env, side).fetch(request);
      const headers = new Headers(res.headers);
      for (const [key, value] of Object.entries(SECURITY_HEADERS)) headers.set(key, value);
      return new Response(res.body, { status: res.status, headers });
    }
    // API answers are public and say how long they keep (Cache-Control), so
    // repeats are served from the edge and never reach Postgres.
    // So are the discovery files under /.well-known/ (internal/site/agents.go):
    // the same for every caller, and asked for by every agent that connects.
    const cacheable = !testing && request.method === "GET" && /^\/(?:(?:en\/)?api\/v1|\.well-known)\//.test(url.pathname);
    const cacheURL = new URL(url);
    cacheURL.searchParams.set("__origin_side", side);
    const cacheKey = new Request(cacheURL.toString(), { method: "GET" });
    let res = cacheable ? await caches.default.match(cacheKey) : undefined;
    const hit = !!res;
    if (!res) {
      res = await originService(env, side).fetch(request);
      if (cacheable && res.status === 200 && !/no-store|private/.test(res.headers.get("Cache-Control") || "")) {
        ctx?.waitUntil(caches.default.put(cacheKey, res.clone()));
      }
    }
    // A cached answer carries the Server-Timing of the request that filled
    // the cache: not this request's, so it is not passed on.
    const headers = new Headers(res.headers);
    if (hit) headers.delete("Server-Timing");
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
