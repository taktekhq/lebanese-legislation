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
import { SECURITY_HEADERS, EMBEDDABLE, AGENT_LINKS } from "./headers.js";
import { toMarkdown, prefersMarkdown } from "./markdown.js";
import { originFetch, isTestRequest } from "./origin.js";
import { markdownAsk } from "./markdownask.js";
import { isParts, parseParts, prepareFrame, assemble, indexPointer } from "./layout.js";

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

// The frames pages in parts are put into (layout.js). _export/layout.json
// names the frame of each lang/schema and is read like the generation; a
// frame object never changes, so a Worker keeps the few it has read. The
// pointer's ETag is part of the page cache key, so a new frame shows at once.
// With no pointer (before the first export in parts) the key is as before.
let pointer = { at: 0, value: null };

async function layoutPointer(env) {
  if (pointer.at && Date.now() - pointer.at < GENERATION_TTL_MS) return pointer.value;
  const obj = await env.SITE.get("_export/layout.json");
  pointer = { at: Date.now(), value: obj ? { etag: obj.etag || obj.httpEtag || "", ids: indexPointer(await obj.json()) } : null };
  return pointer.value;
}

const FRAMES_KEPT = 16;
const frames = new Map();

async function frameFor(env, key) {
  const p = await layoutPointer(env);
  const id = p && p.ids.get(key);
  if (!id) throw new Error("no frame for pages in parts " + key);
  let f = frames.get(id);
  if (!f) {
    const obj = await env.SITE.get("_layout/" + id + ".json");
    if (!obj) throw new Error("frame object missing: " + id);
    f = { id, uploaded: obj.uploaded, ...prepareFrame(await obj.json()) };
    if (frames.size >= FRAMES_KEPT) frames.delete(frames.keys().next().value);
    frames.set(id, f);
  }
  return f;
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

// notModified reports whether a conditional request already holds the page
// these headers describe. If-None-Match wins when sent, compared the weak way
// (Cloudflare marks an ETag W/ when it compresses the body, and crawlers send
// back what they got); otherwise If-Modified-Since against Last-Modified.
// Googlebot sends both, and a 304 spares it the body of a page that did not
// change, so its visits go to pages that did.
export function notModified(request, headers) {
  const inm = request.headers.get("If-None-Match");
  if (inm) {
    const etag = headers.get("ETag");
    if (!etag) return false;
    const bare = (t) => t.trim().replace(/^W\//, "");
    return inm.trim() === "*" || inm.split(",").some((t) => bare(t) === bare(etag));
  }
  const ims = Date.parse(request.headers.get("If-Modified-Since") || "");
  const lm = Date.parse(headers.get("Last-Modified") || "");
  return !Number.isNaN(ims) && !Number.isNaN(lm) && lm <= ims;
}

function notModifiedResponse(headers) {
  const h = new Headers(headers);
  h.delete("Content-Length");
  h.delete("Content-Encoding");
  return new Response(null, { status: 304, headers: h });
}

// lastModified is the later of the dates given (R2 upload times: the uploader
// skips a page whose bytes did not change, so it is when the page last
// changed), as an HTTP date, or "" when none is known.
function lastModified(...dates) {
  const t = Math.max(...dates.map((d) => (d instanceof Date ? d.getTime() : NaN)).filter((n) => !Number.isNaN(n)));
  return Number.isFinite(t) ? new Date(t).toUTCString() : "";
}

function withHeaders(obj, request, body = obj.body, etag = obj.httpEtag, modified = lastModified(obj.uploaded), markdown = false) {
  const h = new Headers(SECURITY_HEADERS);
  if (EMBEDDABLE.has(new URL(request.url).pathname)) {
    h.set("Cross-Origin-Resource-Policy", "cross-origin");
    h.set("Access-Control-Allow-Origin", "*");
  }
  const type = obj.httpMetadata?.contentType || "application/octet-stream";
  h.set("Content-Type", markdown ? "text/markdown; charset=utf-8" : type);
  if (type.startsWith("text/html")) {
    // A page has two versions, HTML and Markdown, chosen by the Accept
    // header; both say where the machine-readable descriptions are.
    h.set("Vary", "Accept");
    h.set("Link", AGENT_LINKS);
  }
  if (markdown) {
    h.set("X-Markdown-Tokens", String(Math.ceil(body.length / 4))); // an estimate: about four characters a token
    h.set("Access-Control-Allow-Origin", "*");
    h.set("Cross-Origin-Resource-Policy", "cross-origin");
  }
  h.set("Cache-Control", obj.httpMetadata?.cacheControl || "public, max-age=300");
  h.set("ETag", etag);
  if (modified) h.set("Last-Modified", modified);
  h.set("X-Served-By", "static"); // for the cutover; drop it once routes are final
  if (notModified(request, h)) return notModifiedResponse(h);
  return new Response(request.method === "HEAD" ? null : body, { status: 200, headers: h });
}

// pageResponse answers with a stored object. An HTML page may be stored
// whole (every page before the edge layout, and pages outside the layout)
// or in parts; the bytes tell which. Its ETag names the frame too, so a new
// footer is a new ETag.
//
// With markdown set (the request prefers text/markdown), the same page is
// answered as Markdown, converted here from the HTML (markdown.js).
export async function pageResponse(obj, request, env, markdown = false) {
  if (!(obj.httpMetadata?.contentType || "").startsWith("text/html")) return withHeaders(obj, request);
  let bytes = new Uint8Array(await obj.arrayBuffer());
  let etag = obj.httpEtag, modified = lastModified(obj.uploaded);
  if (isParts(bytes)) {
    const page = parseParts(bytes);
    const frame = await frameFor(env, page.key);
    bytes = assemble(frame, page);
    etag = obj.httpEtag.replace(/"$/, "") + "-" + frame.id.slice(0, 12) + '"';
    modified = lastModified(obj.uploaded, frame.uploaded);
  }
  if (!markdown) return withHeaders(obj, request, bytes, etag, modified);
  const url = new URL(request.url);
  const md = toMarkdown(new TextDecoder().decode(bytes), url.origin + url.pathname);
  return withHeaders(obj, request, md, etag.replace(/"$/, "") + '-md"', modified, true);
}

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    // Count requests that ask for Markdown (sent after the response; nothing for others).
    markdownAsk(request, env, ctx);
    // The exporter asks this before it stores pages in parts: an older Worker
    // answers 404 here and would serve the parts as they are.
    if (url.pathname === "/_build/layout") {
      return new Response("LLP1\n", { status: 200, headers: { ...SECURITY_HEADERS, "Content-Type": "text/plain", "Cache-Control": "no-store" } });
    }
    if (url.pathname.startsWith("/_build/") || url.pathname.startsWith("/_export/") || url.pathname.startsWith("/_layout/")) {
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
    // So does the A2A lookup at /a2a (JSON-RPC over POST).
    const mcpPath = url.pathname === "/mcp" || url.pathname === "/mcp/" || url.pathname === "/a2a";
    if (!mcpPath && request.method !== "GET" && request.method !== "HEAD" && !(request.method === "POST" && postAllowed)) {
      return new Response(null, { status: 405, headers: { ...SECURITY_HEADERS, Allow: postAllowed ? "GET, HEAD, POST" : "GET, HEAD" } });
    }
    if (isTestRequest(request, env)) {
      return originFetch(request, env, ctx);
    }
    try {
      if (route(request.method, url.pathname, url.search) === "origin") return originFetch(request, env, ctx);
      const key = keyFor(url.pathname);
      if (key) {
        const cache = caches.default;
        const gen = await exportGeneration(env);
        const layout = await layoutPointer(env);
        // A request that prefers Markdown gets the page converted, kept
        // under its own cache key. A browser never asks for it.
        const markdown = prefersMarkdown(request.headers.get("Accept"));
        const q = new URLSearchParams();
        if (gen) q.set("__export", gen);
        if (layout) q.set("__layout", layout.etag);
        if (markdown) q.set("__markdown", "1");
        const qs = q.toString();
        const cacheKey = new Request(url.origin + url.pathname + (qs ? "?" + qs : ""), { method: "GET" });
        const hit = await cache.match(cacheKey);
        if (hit && request.method === "GET") return notModified(request, hit.headers) ? notModifiedResponse(hit.headers) : hit;
        const obj = await env.SITE.get(key);
        if (obj) {
          const res = await pageResponse(obj, request, env, markdown);
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
