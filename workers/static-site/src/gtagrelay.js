// The /gtag/js relay. The site's Content-Security-Policy no longer lists
// www.googletagmanager.com as a script source: that host serves Google Tag
// Manager containers for any id, so listing it lets an injected script load
// someone else's container. The page asks for gtag.js here instead, and the
// Worker fetches only the site's own measurement id.

// The measurement id in internal/site/static/assets/load.js (a test keeps them equal).
export const GA_ID = "G-RDYRK422NP";

export async function gtagRelay(request, notFoundHeaders) {
  const url = new URL(request.url);
  const params = [...url.searchParams];
  if (url.pathname !== "/gtag/js" || params.length !== 1 || params[0][0] !== "id" || params[0][1] !== GA_ID) {
    return new Response(null, { status: 404, headers: notFoundHeaders });
  }
  // The crawler (botVisitor below) gets no gtag.js, so GA4 does not count it.
  if (botVisitor(request)) {
    return new Response(null, { status: 204, headers: { ...notFoundHeaders, "Cache-Control": "no-store" } });
  }
  const res = await fetch(`https://www.googletagmanager.com/gtag/js?id=${GA_ID}`, {
    method: "GET",
    headers: { accept: "*/*", "accept-encoding": request.headers.get("accept-encoding") || "identity" },
    redirect: "manual",
    cf: { cacheTtl: 3600, cacheEverything: true },
  });
  if (res.status !== 200) return new Response(null, { status: 502, headers: notFoundHeaders });
  const out = new Headers();
  for (const name of ["content-type", "content-encoding", "etag", "last-modified"]) {
    const v = res.headers.get(name);
    if (v !== null) out.set(name, v);
  }
  out.set("Content-Type", "application/javascript; charset=UTF-8");
  out.set("Cache-Control", "public, max-age=3600");
  out.set("X-Content-Type-Options", "nosniff");
  out.set("Cross-Origin-Resource-Policy", "same-origin");
  return new Response(res.body, { status: 200, headers: out });
}

// Visitors the analytics never see: they get no gtag.js, so GA4 does not count them.
// From 10 Nov 2026 a crawler that runs JavaScript loaded one page per visit with
// a zh-CN browser, a new IP each time and no cookie, so every hit counted as a
// new visitor: 8,804 page view persons on 16 Nov, 8,497 of them zh-CN, against
// about 300 real readers. It came first from Huawei Cloud and Tencent Cloud,
// then, once those were dropped, from home addresses across Europe (a
// residential proxy pool). Real readers do not browse from these cloud
// networks, and before 10 Nov at most two a day had a Chinese browser.
export const BOT_ASNS = new Set([
  136907, 55990, 265443, // Huawei Cloud
  132203, 45090,         // Tencent Cloud
  45102, 37963,          // Alibaba Cloud
]);

// botNetwork reports whether a request's Cloudflare cf object names a network
// whose visitors are crawlers. A request without cf (tests, wrangler dev) is not.
export function botNetwork(cf) {
  if (!cf) return false;
  return BOT_ASNS.has(Number(cf.asn)) || cf.country === "CN";
}

// botVisitor adds the browser's first language: Chinese means the crawler,
// whatever network it comes from.
export function botVisitor(request) {
  const lang = (request.headers.get("accept-language") || "").split(/[,;]/)[0].trim().toLowerCase();
  return botNetwork(request.cf) || lang === "zh" || lang.startsWith("zh-");
}
