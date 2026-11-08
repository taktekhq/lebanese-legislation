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
