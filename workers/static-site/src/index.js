import { DurableObject } from "cloudflare:workers";
// Empty placeholders for the old container classes until their migration can delete them.
export class Origin extends DurableObject {}
export class OriginBlue extends DurableObject {}
export class OriginGreen extends DurableObject {}
// lebaneselegislation.com from files in R2.
//
// A request is answered from the bucket when it is a GET or HEAD for a page
// the exporter wrote. Anything else goes to the live origin, unchanged:
// searches, the API, MCP, the analytics proxy, files read from disk, filters,
// and every address no file and no redirect rule answers (the origin then
// answers as it does today, a 404 included). So the Worker can take one route
// at a time, and removing its route puts the origin back in charge.

import { route, keyFor, resolve, indexRedirectData } from "./route.js";
import { SECURITY_HEADERS } from "./headers.js";
import { originFetch } from "./origin.js";

// The export generation (the workflow run that last finished uploading) is
// part of the page cache key: pages are cached for 12 hours, and CI cannot
// purge, so without it a new build stays hidden behind the old copies.
const GENERATION_TTL_MS = 60 * 1000;
let generation = { at: 0, value: "" };

async function exportGeneration(env) {
  if (generation.at && Date.now() - generation.at < GENERATION_TTL_MS) return generation.value;
  const obj = await env.SITE.get("_export/generation");
  generation = { at: Date.now(), value: obj ? (await obj.text()).trim() : "" };
  return generation.value;
}

const REDIRECT_DATA_TTL_MS = 10 * 60 * 1000;
let redirects = { at: 0, data: null };

async function redirectData(env) {
  if (redirects.data && Date.now() - redirects.at < REDIRECT_DATA_TTL_MS) return redirects.data;
  const obj = await env.SITE.get("_export/redirects.json");
  if (!obj) return null;
  redirects = { at: Date.now(), data: indexRedirectData(await obj.json()) };
  return redirects.data;
}

function withHeaders(obj, request) {
  const h = new Headers(SECURITY_HEADERS);
  h.set("Content-Type", obj.httpMetadata?.contentType || "application/octet-stream");
  h.set("Cache-Control", obj.httpMetadata?.cacheControl || "public, max-age=300");
  h.set("ETag", obj.httpEtag);
  h.set("X-Served-By", "static"); // for the cutover; drop it once routes are final
  const match = request.headers.get("If-None-Match");
  if (match && match === obj.httpEtag) return new Response(null, { status: 304, headers: h });
  return new Response(request.method === "HEAD" ? null : obj.body, { status: 200, headers: h });
}

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    if (url.pathname.startsWith("/_build/") || url.pathname.startsWith("/_export/")) {
      return new Response(null, { status: 404, headers: SECURITY_HEADERS });
    }
    if (url.hostname === "www.lebaneselegislation.com") {
      return new Response(null, {
        status: 301,
        headers: { ...SECURITY_HEADERS, Location: "https://lebaneselegislation.com" + url.pathname + url.search },
      });
    }
    const postAllowed = /^(?:\/(?:en|ar))?\/(?:search\/|search\/answer\/|search\/ai\/|search\/ai\/deep\/)$/.test(url.pathname)
      || url.pathname.startsWith("/ph/");
    // MCP (streamable HTTP) uses POST, GET, DELETE and OPTIONS; the server checks them itself.
    const mcpPath = url.pathname === "/mcp" || url.pathname === "/mcp/";
    if (!mcpPath && request.method !== "GET" && request.method !== "HEAD" && !(request.method === "POST" && postAllowed)) {
      return new Response(null, { status: 405, headers: { ...SECURITY_HEADERS, Allow: postAllowed ? "GET, HEAD, POST" : "GET, HEAD" } });
    }
    if (/shapbot/i.test(request.headers.get("User-Agent") || "")) return new Response(null, { status: 429 });
    if (env.ORIGIN_TEST_KEY && request.headers.get("x-origin-test") === env.ORIGIN_TEST_KEY) {
      return originFetch(request, env, ctx);
    }
    if (route(request.method, url.pathname, url.search) === "origin") return originFetch(request, env, ctx);

    try {
      const key = keyFor(url.pathname);
      if (key) {
        const cache = caches.default;
        const gen = await exportGeneration(env);
        const cacheKey = new Request(url.origin + url.pathname + (gen ? "?__export=" + encodeURIComponent(gen) : ""), { method: "GET" });
        const hit = await cache.match(cacheKey);
        if (hit && request.method === "GET") return hit;
        const obj = await env.SITE.get(key);
        if (obj) {
          const res = withHeaders(obj, request);
          if (res.status === 200 && request.method === "GET") ctx.waitUntil(cache.put(cacheKey, res.clone()));
          return res;
        }
        // no page here: an old address moves in one hop, as it does on the origin
        const data = await redirectData(env);
        if (data) {
          const to = await resolve(url.pathname, data, async (p) => {
            const k = keyFor(p);
            return k !== null && (await env.SITE.head(k)) !== null;
          });
          if (to !== url.pathname) {
            return new Response(null, { status: 301, headers: { Location: to + url.search, "Cache-Control": "public, max-age=3600" } });
          }
        }
      }
    } catch (err) {
      console.error("static site: falling back to the origin:", err && err.message);
    }
    return originFetch(request, env, ctx);
  },
};
