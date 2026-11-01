// Live stat badges (shields.io style SVGs) for pages we publish but can't
// put gtag on, such as the Hugging Face dataset card (Markdown, no
// scripts). Each badge shows a current number from the D1 snapshot, so
// the card never goes stale between uploads.
//
// One badge per page also counts the view: the card adds ?src=hf to
// exactly one of its badges, and that request records a GA4 page_view in
// the page's own stream. Hugging Face doesn't proxy external images, so
// each visitor's browser fetches the badge itself. Badges without src
// (e.g. in a GitHub README, which GitHub proxies through its image cache
// and would miscount) only show the number.

import { GAEvent, GAStream, hfStream, track } from "./ga";
import { phCapture } from "./posthog";
import { hfLastModified } from "./hfstats";

export interface BadgePage {
  location: string;
  title: string;
  stream: (env: Env) => GAStream;
  // The page's real address, for PostHog (GA needs a lebaneselegislation.com
  // location; PostHog takes the page as it is).
  url: string;
}

// Pages whose views a badge may count, by ?src= value.
export const BADGE_PAGES: Record<string, BadgePage> = {
  // location must be on the stream's own domain (lebaneselegislation.com):
  // GA dropped page_views whose page_location was huggingface.co. The
  // stream itself says which page this is.
  hf: {
    location: "https://lebaneselegislation.com/huggingface",
    title: "Lebanese Legislation dataset (Hugging Face)",
    stream: hfStream,
    url: "https://huggingface.co/datasets/taktekhq/lebanese-legislation",
  },
};

export interface Stats {
  legislation: number;
  articles: number;
  rulings: number;
}

// The counts read every row of three big tables (about 650,000 rows), so
// they are cached at the edge; they only change with the nightly snapshot.
const STATS_TTL_SECONDS = 6 * 60 * 60;
const STATS_CACHE_KEY = "https://lebaneselegislation.com/__badge_stats__/v2";

export async function loadStats(db: D1Database): Promise<Stats> {
  const cache = (caches as unknown as WorkersCacheStorage).default;
  const hit = await cache.match(STATS_CACHE_KEY);
  if (hit) return hit.json();
  const row = await db
    .prepare(
      `SELECT (SELECT COUNT(*) FROM legislation) AS legislation,
              (SELECT COUNT(*) FROM legislation_article) AS articles,
              (SELECT COUNT(*) FROM ruling) AS rulings`,
    )
    .first<Stats>();
  const stats: Stats = {
    legislation: row?.legislation ?? 0,
    articles: row?.articles ?? 0,
    rulings: row?.rulings ?? 0,
  };
  await cache.put(
    STATS_CACHE_KEY,
    new Response(JSON.stringify(stats), {
      headers: { "Content-Type": "application/json", "Cache-Control": `public, max-age=${STATS_TTL_SECONDS}` },
    }),
  );
  return stats;
}

export function thousands(n: number): string {
  return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

interface BadgeDef {
  label: string;
  value: (s: Stats) => string;
  needsStats: boolean;
  // A value read from somewhere other than D1 (the Hugging Face API).
  load?: () => Promise<string>;
}

export const BADGES: Record<string, BadgeDef> = {
  legislation: { label: "legislation", value: (s) => `${thousands(s.legislation)} texts`, needsStats: true },
  articles: { label: "articles", value: (s) => thousands(s.articles), needsStats: true },
  rulings: { label: "court rulings", value: (s) => thousands(s.rulings), needsStats: true },
  mcp: { label: "MCP server", value: () => "online", needsStats: false },
  updated: { label: "last updated", value: () => "", needsStats: false, load: async () => day(await hfLastModified()) },
};

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

// day writes an ISO timestamp as "20 Oct 2026" (UTC).
export function day(iso: string): string {
  const d = new Date(iso);
  return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
}

const LABEL_COLOR = "#555";
const VALUE_COLOR = "#2b5c97"; // the site's --accent
const MUTED_COLOR = "#9f9f9f"; // when the number couldn't be read

// textWidth approximates Verdana 11px, which is what the SVG asks for;
// close enough that the text never overflows its box.
export function textWidth(s: string): number {
  let w = 0;
  for (const ch of s) {
    if ("il.,:;|!'1 ".includes(ch)) w += 3.9;
    else if ("mwMW".includes(ch)) w += 10;
    else if (/[0-9]/.test(ch)) w += 7;
    else if (/[A-Z]/.test(ch)) w += 7.6;
    else w += 6.4;
  }
  return Math.ceil(w);
}

function esc(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

export function renderBadge(label: string, value: string, color = VALUE_COLOR): string {
  const lw = textWidth(label) + 12;
  const vw = textWidth(value) + 12;
  const w = lw + vw;
  const l = esc(label);
  const v = esc(value);
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="20" role="img" aria-label="${l}: ${v}">` +
    `<title>${l}: ${v}</title>` +
    `<linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>` +
    `<clipPath id="r"><rect width="${w}" height="20" rx="3" fill="#fff"/></clipPath>` +
    `<g clip-path="url(#r)"><rect width="${lw}" height="20" fill="${LABEL_COLOR}"/><rect x="${lw}" width="${vw}" height="20" fill="${color}"/><rect width="${w}" height="20" fill="url(#s)"/></g>` +
    `<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">` +
    `<text x="${lw / 2}" y="15" fill="#010101" fill-opacity=".3">${l}</text><text x="${lw / 2}" y="14">${l}</text>` +
    `<text x="${lw + vw / 2}" y="15" fill="#010101" fill-opacity=".3">${v}</text><text x="${lw + vw / 2}" y="14">${v}</text>` +
    `</g></svg>`;
}

// Crawlers and link previewers that fetch images are not readers.
const BOT_UA = /bot|crawl|spider|slurp|preview|fetch|curl|wget|python|httpx|go-http|java\/|headless/i;

export function isBot(ua: string): boolean {
  return ua === "" || BOT_UA.test(ua);
}

function svgResponse(svg: string, status = 200): Response {
  return new Response(svg, {
    status,
    headers: {
      "Content-Type": "image/svg+xml; charset=utf-8",
      // Every view must reach the Worker (for the count and the live
      // number), not a browser or edge cache.
      "Cache-Control": "no-store, max-age=0",
      "Access-Control-Allow-Origin": "*",
    },
  });
}

export function viewEvent(page: BadgePage, badge: string, referrer: string): GAEvent {
  return {
    name: "page_view",
    params: { page_location: page.location, page_title: page.title, page_referrer: referrer, badge },
  };
}

// handleBadge serves /badge/<name>.svg[?src=<page>].
export async function handleBadge(request: Request, url: URL, env: Env, ctx?: ExecutionContext): Promise<Response> {
  if (request.method !== "GET" && request.method !== "HEAD") {
    return new Response(null, { status: 405, headers: { Allow: "GET, HEAD" } });
  }
  const m = /^\/badge\/([a-z]+)\.svg$/.exec(url.pathname);
  const def = m && Object.hasOwn(BADGES, m[1]) ? BADGES[m[1]] : undefined;
  if (!m || !def) return svgResponse(renderBadge("badge", "not found", MUTED_COLOR), 404);

  const src = url.searchParams.get("src") ?? "";
  const page = Object.hasOwn(BADGE_PAGES, src) ? BADGE_PAGES[src] : undefined;
  if (page && request.method === "GET" && !isBot(request.headers.get("User-Agent") ?? "")) {
    track(page.stream(env), ctx, request, [viewEvent(page, m[1], request.headers.get("Referer") ?? "")]);
    phCapture(env, ctx, request, [{
      event: "$pageview",
      properties: {
        $current_url: page.url, $host: new URL(page.url).host, $pathname: new URL(page.url).pathname, $title: page.title,
        $referrer: request.headers.get("Referer") ?? undefined, $user_agent: request.headers.get("User-Agent") ?? "", badge: m[1],
      },
    }]);
  }

  if (def.load) {
    try {
      return svgResponse(renderBadge(def.label, await def.load()));
    } catch (e) {
      console.error(`badge ${m[1]}:`, e);
      return svgResponse(renderBadge(def.label, "n/a", MUTED_COLOR));
    }
  }
  if (!def.needsStats) return svgResponse(renderBadge(def.label, def.value({} as Stats)));
  try {
    return svgResponse(renderBadge(def.label, def.value(await loadStats(env.DB))));
  } catch (e) {
    console.error("badge stats:", e);
    return svgResponse(renderBadge(def.label, "n/a", MUTED_COLOR));
  }
}
