// The /ph/ analytics relay. It forwards to PostHog only what the site's own
// integration (internal/site/static/ph.js and the posthog-js it loads)
// requests, and only for the site's own project. Anything else is a 404 that
// never reaches PostHog, so same-origin /ph/ addresses can only ever serve
// PostHog's library files and our own project's answers.

// The project token in internal/site/static/ph.js (a test keeps them equal).
export const PH_TOKEN = "phc_pTpSY5wVsQrG8LN58z4zWXdMWjvqR6uPCY2eXV25sBV5";

const API_PATHS = [
  /^\/e\/?$/,              // events (the default capture endpoint)
  /^\/i\/v0\/e\/?$/,       // events (the endpoint remote config may choose)
  /^\/s\/?$/,              // session replay
  /^\/batch\/?$/,          // batched events
  /^\/flags\/?$/,          // feature flags and config
  /^\/decide\/?$/,         // flags, older library versions
  /^\/i\/v1\/(logs|metrics)\/?$/,
];
// Endpoints that name the project in ?token=.
const TOKEN_API_PATHS = /^\/api\/(surveys|early_access_features|web_experiments|product_tours)\/?$/;
// The library and its lazy extensions: /static/array.js, /static/<version>/recorder.js.
const STATIC_PATH = /^\/static\/(?:[0-9][0-9a-z.+-]{0,40}\/)?[a-z0-9][a-z0-9._-]{0,80}\.(js|css)$/i;
const CONFIG_PATH = /^\/array\/([^/]+)\/(config|config\.js)$/;

// Request headers PostHog needs. Everything else (cookies, authorization,
// our own x-origin-* headers, Cloudflare's headers) stays here.
const FORWARD_HEADERS = [
  "accept", "accept-encoding", "accept-language", "content-encoding", "content-type",
  "if-modified-since", "if-none-match", "origin", "referer", "user-agent",
];

// phTarget returns the PostHog URL for an allowed /ph/ address, else null.
export function phTarget(url) {
  if (!url.pathname.startsWith("/ph/")) return null;
  const path = url.pathname.slice(3);
  // Any project token other than ours, anywhere in the address, is refused.
  const tokens = (url.pathname + "?" + decodeURIComponentSafe(url.search)).match(/phc_[A-Za-z0-9_-]+/g) || [];
  if (tokens.some(t => t !== PH_TOKEN)) return null;
  for (const key of ["token", "api_key"]) {
    for (const value of url.searchParams.getAll(key)) if (value !== PH_TOKEN) return null;
  }
  let host = "https://eu.i.posthog.com";
  if (STATIC_PATH.test(path)) {
    host = "https://eu-assets.i.posthog.com";
  } else if (TOKEN_API_PATHS.test(path)) {
    if (url.searchParams.get("token") !== PH_TOKEN) return null;
  } else {
    const config = CONFIG_PATH.exec(path);
    if (config) {
      if (config[1] !== PH_TOKEN) return null;
    } else if (!API_PATHS.some(re => re.test(path))) {
      return null;
    }
  }
  return host + path + url.search;
}

function decodeURIComponentSafe(s) {
  try { return decodeURIComponent(s); } catch { return s; }
}

export async function phRelay(request, notFoundHeaders) {
  const url = new URL(request.url);
  const target = phTarget(url);
  if (!target) return new Response(null, { status: 404, headers: notFoundHeaders });
  const headers = new Headers();
  for (const name of FORWARD_HEADERS) {
    const value = request.headers.get(name);
    if (value !== null) headers.set(name, value);
  }
  // The visitor's address only, for PostHog's location lookup.
  const ip = request.headers.get("cf-connecting-ip");
  if (ip) headers.set("x-forwarded-for", ip);
  const hasBody = request.method !== "GET" && request.method !== "HEAD";
  const res = await fetch(target, {
    method: request.method,
    headers,
    body: hasBody ? await request.arrayBuffer() : undefined,
    redirect: "manual",
  });
  const out = new Headers(res.headers);
  out.delete("set-cookie");
  out.set("X-Content-Type-Options", "nosniff");
  out.set("Cross-Origin-Resource-Policy", "same-origin");
  return new Response(res.body, { status: res.status, statusText: res.statusText, headers: out });
}
