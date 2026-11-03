// Load the site in a real browser, desktop and phone, and check what GA4 and
// PostHog would receive. Nothing is recorded: every analytics hit is answered
// here and never reaches Google or PostHog, so the check adds no fake visits.
//
//   BASE_URL=https://lebaneselegislation.com node scripts/analytics-smoke.cjs
//
//   BASE_URL           the site (default https://lebaneselegislation.com)
//   PAGES              comma-separated paths (default "/,/blog/,/search/?q=عمل")
//   PLAYWRIGHT_MODULE  where to require Playwright from (default "playwright")
//   CHROME_PATH        a Chromium binary, if Playwright's own is not installed
//
// /ph/* is fetched from PostHog directly, so a local server without the analytics
// proxy works too. It checks, per device and page:
//   GA4      a page_view for G-RDYRK422NP with the page's real title and the
//            full address (utm_source kept), and no ep.source/medium/campaign
//   PostHog  a $pageview from posthog-js ($lib "web") with the same address
//   named    the event the page sends besides the page view, in both tools:
//            "search" with search_term, "blog_read" or "document_open"
// Exit 0 when all pass, 1 otherwise. Needs Node 18.17 or newer.
"use strict";
const zlib = require("zlib");
const { chromium, devices } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

const base = (process.env.BASE_URL || "https://lebaneselegislation.com").replace(/\/$/, "");
const pages = (process.env.PAGES || "/,/blog/,/search/?q=عمل").split(",");
const PH_EVENTS = /^\/ph\/(e|i\/v0\/e|batch|capture|track|engage|s)\/?$/;

// GA4 batches events: shared parameters in the URL, one line of the body per event.
function gaEvents(url, body) {
  const shared = Object.fromEntries(new URL(url).searchParams);
  const lines = (body || "").split("\n").filter(Boolean);
  if (!lines.length) return [shared];
  return lines.map((l) => ({ ...shared, ...Object.fromEntries(new URLSearchParams(l)) }));
}

function phEvents(url, buf) {
  if (!buf || !buf.length) return [];
  let text;
  if (/compression=gzip-js/.test(url) || (buf[0] === 0x1f && buf[1] === 0x8b)) text = zlib.gunzipSync(buf).toString();
  else {
    text = buf.toString();
    const form = new URLSearchParams(text).get("data");
    if (form) text = Buffer.from(form, "base64").toString();
  }
  const data = JSON.parse(text);
  const list = Array.isArray(data) ? data : data.batch || [data];
  return list.map((e) => ({ event: e.event, properties: e.properties || {} }));
}

async function visit(browser, device, path) {
  // Both tools skip browsers that say they are automated or headless.
  const desktop = { viewport: { width: 1366, height: 900 }, userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36" };
  const context = await browser.newContext(device === "mobile" ? devices["iPhone 13"] : desktop);
  const page = await context.newPage();
  const ga = [], ph = [];
  await page.route(/google-analytics\.com\/g\/collect|analytics\.google\.com\/g\/collect/, (route) => {
    const r = route.request();
    ga.push(...gaEvents(r.url(), r.postData()));
    return route.fulfill({ status: 204, body: "" });
  });
  await page.route((u) => u.origin === base && u.pathname.startsWith("/ph/"), async (route) => {
    const r = route.request(), u = new URL(r.url());
    if (PH_EVENTS.test(u.pathname)) {
      try { ph.push(...phEvents(r.url(), r.postDataBuffer())); } catch (err) { ph.push({ event: "unreadable", error: String(err) }); }
      return route.fulfill({ status: 200, contentType: "application/json", body: '{"status":1}' });
    }
    const upstream = (u.pathname.startsWith("/ph/static/") ? "https://eu-assets.i.posthog.com" : "https://eu.i.posthog.com") + u.pathname.slice(3) + u.search;
    try {
      const response = await route.fetch({ url: upstream });
      await route.fulfill({ response });
    } catch (_) {} // the page was closed while PostHog fetched its settings
  });

  const sep = path.includes("?") ? "&" : "?";
  const url = base + path + sep + "utm_source=chatgpt.com";
  await page.goto(url, { waitUntil: "load" });
  await page.mouse.move(200, 200);
  const title = await page.title();
  const deadline = Date.now() + 20000;
  const isSearch = path.startsWith("/search/");
  // The named event each kind of page sends besides the page view.
  const bare = path.replace(/^\/en\//, "/");
  const named = isSearch ? "search" : /^\/blog\/[^/]+\//.test(bare) ? "blog_read"
    : /^\/(law|decree|text|ruling|gazette|opinion|agreement|doctrine|research)\/\d/.test(bare) ? "document_open" : null;
  const done = () => ga.some((e) => e.en === "page_view") && ph.some((e) => e.event === "$pageview")
    && (!named || (ga.some((e) => e.en === named) && ph.some((e) => e.event === named)));
  while (!done() && Date.now() < deadline) await page.waitForTimeout(250);
  await context.close();

  const fails = [];
  const pv = ga.find((e) => e.en === "page_view");
  if (!pv) fails.push("GA4: no page_view");
  else {
    if (pv.tid !== "G-RDYRK422NP") fails.push(`GA4: tid ${pv.tid}`);
    if (pv.dt !== title) fails.push(`GA4: title ${JSON.stringify(pv.dt)}, page says ${JSON.stringify(title)}`);
    if (!/utm_source=chatgpt\.com/.test(pv.dl || "")) fails.push(`GA4: address lost utm_source: ${pv.dl}`);
  }
  for (const e of ga) {
    for (const k of ["ep.source", "ep.medium", "ep.campaign"]) if (k in e) fails.push(`GA4: ${e.en} carries ${k}=${e[k]}`);
  }
  const ppv = ph.find((e) => e.event === "$pageview");
  if (!ppv) fails.push("PostHog: no $pageview");
  else {
    if (ppv.properties.$lib !== "web") fails.push(`PostHog: $lib ${ppv.properties.$lib}, want web (posthog-js)`);
    if (!/utm_source=chatgpt\.com/.test(ppv.properties.$current_url || "")) fails.push(`PostHog: $current_url ${ppv.properties.$current_url}`);
  }
  if (named) {
    const gn = ga.find((e) => e.en === named), pn = ph.find((e) => e.event === named);
    if (!gn) fails.push(`GA4: no ${named}`);
    if (!pn) fails.push(`PostHog: no ${named}`);
    if (isSearch && !(gn && gn["ep.search_term"] && pn && pn.properties.search_term)) fails.push("search: search_term missing");
  }
  return {
    device, path, title, fails,
    ga: [...new Set(ga.map((e) => e.en))],
    posthog: [...new Set(ph.map((e) => e.event))],
  };
}

(async () => {
  const opts = { args: ["--disable-blink-features=AutomationControlled"] };
  if (process.env.CHROME_PATH) opts.executablePath = process.env.CHROME_PATH;
  const browser = await chromium.launch(opts);
  let failed = 0;
  try {
    for (const device of ["desktop", "mobile"]) {
      for (const path of pages) {
        const r = await visit(browser, device, path);
        failed += r.fails.length;
        console.log(`${r.fails.length ? "FAIL" : "ok  "} ${device.padEnd(7)} ${path}  GA4 [${r.ga.join(" ")}]  PostHog [${r.posthog.join(" ")}]`);
        for (const f of r.fails) console.log(`       ${f}`);
      }
    }
  } finally {
    await browser.close();
  }
  process.exit(failed ? 1 : 0);
})();
