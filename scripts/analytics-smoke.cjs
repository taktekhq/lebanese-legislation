// Load the site in a real browser, desktop and phone, and check what GA4
// would receive. Nothing is recorded: every analytics hit is answered here
// and never reaches Google, so the check adds no fake visits.
//
//   BASE_URL=https://lebaneselegislation.com node scripts/analytics-smoke.cjs
//
//   BASE_URL           the site (default https://lebaneselegislation.com)
//   PAGES              comma-separated paths (default "/,/blog/,/search/?q=عمل")
//   PLAYWRIGHT_MODULE  where to require Playwright from (default "playwright")
//   CHROME_PATH        a Chromium binary, if Playwright's own is not installed
//
// It checks, per device and page:
//   GA4      a page_view for G-RDYRK422NP with the page's real title and the
//            full address (utm_source kept), and no ep.source/medium/campaign
//   named    the event the page sends besides the page view:
//            "search" with search_term, "blog_read" or "document_open"
// Exit 0 when all pass, 1 otherwise. Needs Node 18.17 or newer.
"use strict";
const { chromium, devices } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

const base = (process.env.BASE_URL || "https://lebaneselegislation.com").replace(/\/$/, "");
const pages = (process.env.PAGES || "/,/blog/,/search/?q=عمل").split(",");

// GA4 batches events: shared parameters in the URL, one line of the body per event.
function gaEvents(url, body) {
  const shared = Object.fromEntries(new URL(url).searchParams);
  const lines = (body || "").split("\n").filter(Boolean);
  if (!lines.length) return [shared];
  return lines.map((l) => ({ ...shared, ...Object.fromEntries(new URLSearchParams(l)) }));
}

async function visit(browser, device, path) {
  // GA4 skips browsers that say they are automated or headless.
  const desktop = { viewport: { width: 1366, height: 900 }, userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36" };
  const context = await browser.newContext(device === "mobile" ? devices["iPhone 13"] : desktop);
  const page = await context.newPage();
  const ga = [];
  await page.route(/google-analytics\.com\/g\/collect|analytics\.google\.com\/g\/collect/, (route) => {
    const r = route.request();
    ga.push(...gaEvents(r.url(), r.postData()));
    return route.fulfill({ status: 204, body: "" });
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
  const done = () => ga.some((e) => e.en === "page_view") && (!named || ga.some((e) => e.en === named));
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
  if (named) {
    const gn = ga.find((e) => e.en === named);
    if (!gn) fails.push(`GA4: no ${named}`);
    if (isSearch && !(gn && gn["ep.search_term"])) fails.push("search: search_term missing");
  }
  return {
    device, path, title, fails,
    ga: [...new Set(ga.map((e) => e.en))],
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
        console.log(`${r.fails.length ? "FAIL" : "ok  "} ${device.padEnd(7)} ${path}  GA4 [${r.ga.join(" ")}]`);
        for (const f of r.fails) console.log(`       ${f}`);
      }
    }
  } finally {
    await browser.close();
  }
  process.exit(failed ? 1 : 0);
})();
