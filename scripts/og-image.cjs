// Renders the share images drawn in docs/og/*.html to internal/site/static/assets.
// Usage: node scripts/og-image.cjs [name...] (default: contribute). Optional
// PLAYWRIGHT_MODULE and CHROME_PATH.
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

const root = path.join(__dirname, '..');
const names = process.argv.slice(2);

(async () => {
  const browser = await chromium.launch({ headless: true,
    executablePath: process.env.CHROME_PATH || undefined, args: ['--no-sandbox'] });
  try {
    const page = await browser.newPage({ viewport: { width: 1200, height: 630 }, deviceScaleFactor: 1 });
    for (const name of names.length ? names : ['contribute']) {
      for (const lang of ['en', 'ar']) {
        const src = pathToFileURL(path.join(root, 'docs/og', name + '.html')) + '?lang=' + lang;
        await page.goto(src, { waitUntil: 'load' });
        await page.evaluate(() => document.fonts.ready);
        const out = path.join(root, 'internal/site/static/assets', `og-${name}-${lang}.png`);
        await page.screenshot({ path: out, type: 'png' });
        console.log(out);
      }
    }
  } finally {
    await browser.close();
  }
})().catch(err => { console.error(err); process.exit(1); });
