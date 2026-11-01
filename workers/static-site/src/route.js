// Routing rules of the static site's Worker, as pure functions so they can be
// tested without Cloudflare. The redirect rules are a port of Resolve in
// internal/staticexport/redirects.go; both are tested with
// internal/staticexport/testdata/resolve-cases.json.

// Addresses the origin answers by itself: the API, the analytics proxy, the MCP
// endpoint, files read from disk, the pages that need a database.
const ORIGIN_PREFIXES = [
  "/api/", "/ph/", "/ph", "/mcp", "/healthz", "/.well-known/", "/ul/", "/scan/",
  "/ruling-files/", "/official", "/official-files/",
];

// Parameters that identify where a visitor came from and never change a page.
const TRACKING = /^(utm_[a-z0-9_]+|gclid|dclid|fbclid|msclkid|yclid|igshid|mc_cid|mc_eid|ref_src)$/i;

export function hasRealQuery(search) {
  for (const [k] of new URLSearchParams(search)) {
    if (!TRACKING.test(k)) return true;
  }
  return false;
}

// route says where a request goes: "origin" or "static".
export function route(method, pathname, search) {
  if (method !== "GET" && method !== "HEAD") return "origin";
  const bare = pathname.replace(/^\/en(?=\/|$)/, "") || "/";
  if (ORIGIN_PREFIXES.some((p) => bare === p || bare.startsWith(p.endsWith("/") ? p : p + "/") || (p === "/official" && bare.startsWith("/official")))) {
    return "origin";
  }
  // a search with a query goes to the server: it tidies questions, retries with
  // fewer words and offers the AI answer, which the static page cannot do yet
  // a filter or a go-to-article box: the origin builds that page
  if (hasRealQuery(search)) return "origin";
  return "static";
}

// keyFor is the R2 key of a path: pages are <path>/index.html, files keep their
// name. Percent escapes are upper-cased, as the exporter writes them.
export function keyFor(pathname) {
  const p = pathname.replace(/%[0-9a-f]{2}/gi, (m) => m.toUpperCase());
  if (p.split("/").some((s) => s === "..")) return null;
  const key = p === "/" ? "index.html" : p.endsWith("/") ? p.slice(1) + "index.html" : p.slice(1);
  return key.startsWith("_export/") ? null : key;
}

// ---------------------------------------------------------------- redirects

const ADDRESS_WORDS = ["law", "decree", "text"];

export function indexRedirectData(d) {
  const words = new Map();
  for (const [word, list] of [["decree", d.decree || []], ["text", d.text || []]]) {
    for (let i = 0; i + 1 < list.length; i += 2) {
      for (let id = list[i]; id < list[i] + list[i + 1]; id++) words.set(id, word);
    }
  }
  const alias = new Map((d.aliases || []).map(([a, b]) => [a, b]));
  return { words, alias };
}

const wordOf = (data, id) => data.words.get(id) || "law";

function textAddress(p) {
  const parts = p.split("/"); // "", word, id, ...rest
  if (parts.length < 4) return null;
  if (!ADDRESS_WORDS.includes(parts[1])) return null;
  if (!/^[0-9]+$/.test(parts[2]) || Number(parts[2]) <= 0) return null;
  return { word: parts[1], id: Number(parts[2]), rest: parts.slice(3).join("/") };
}

const INDIC = /[٠-٩]/;
const asciiDigits = (s) => s.replace(/[٠-٩]/g, (c) => String(c.charCodeAt(0) - 0x0660));

// resolve applies the live site's moves to an escaped path until none changes
// it. exists(path) is async: does the bucket hold a page there?
export async function resolve(p, data, exists) {
  for (let i = 0; i < 10; i++) {
    let moved = false;
    if (p === "/ar" || p.startsWith("/ar/")) {
      p = "/" + p.slice("/ar".length).replace(/^\/+/, "");
      moved = true;
    }
    const last = p.slice(p.lastIndexOf("/"));
    if (!p.endsWith("/") && !last.includes(".") && (await exists(p + "/"))) {
      p += "/";
      moved = true;
    }
    const a = textAddress(p);
    if (a) {
      const want = wordOf(data, a.id);
      if (want !== a.word) {
        p = `/${want}/${a.id}/${a.rest}`;
        moved = true;
      }
      const to = data.alias.get(a.id);
      if (to !== undefined) {
        p = a.rest.startsWith("article/") ? `/${wordOf(data, to)}/${to}/${a.rest}` : `/${wordOf(data, to)}/${to}/`;
        moved = true;
      }
    }
    const b = textAddress(p);
    if (b && b.rest.startsWith("article/")) {
      const after = b.rest.slice("article/".length);
      const slash = after.indexOf("/");
      if (slash >= 0) {
        let seg = after.slice(0, slash);
        try { seg = decodeURIComponent(seg); } catch { seg = null; }
        if (seg !== null && INDIC.test(seg)) {
          const cand = `/${b.word}/${b.id}/article/${encodeURIComponent(asciiDigits(seg))}/${after.slice(slash + 1)}`;
          if (await exists(cand)) {
            p = cand;
            moved = true;
          }
        }
      }
    }
    if (!moved) break;
  }
  return p;
}
