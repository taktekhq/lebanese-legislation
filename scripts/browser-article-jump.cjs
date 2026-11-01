// Real Chromium regression on a split decree. Requires BASE_URL and Node >=20.
// Optional: PLAYWRIGHT_MODULE, CHROME_PATH, LEGISLATION_SCRIPT (override the served JS).
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const base = process.env.BASE_URL;
if (!base) throw new Error('Set BASE_URL to the site being tested');
(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROME_PATH || undefined });
  try {
    for (const width of [1280, 390]) {
      const page = await browser.newPage({ viewport: { width, height: 832 } });
      if (process.env.LEGISLATION_SCRIPT) {
        await page.route('**/legislation.js?*', r => r.fulfill({
          contentType: 'application/javascript', body: fs.readFileSync(process.env.LEGISLATION_SCRIPT, 'utf8')
        }));
      }
      await page.goto(base + '/decree/244565/', { waitUntil: 'networkidle' });
      await page.locator('[name=n]').fill('86');
      await page.locator('[data-goto] button').click();
      await page.waitForFunction(() => {
        const top = document.querySelector('#a-86 .art__head').getBoundingClientRect().top;
        return location.hash === '#a-86' && top >= 0 && top < 150;
      }, null, { timeout: 5000 });
      assert.equal(new URL(page.url()).hash, '#a-86');
      await page.locator('[name=n]').fill('1000');
      await Promise.all([
        page.waitForURL(/\/article\/1000\//),
        page.locator('[data-goto] button').click()
      ]);
      assert.equal(await page.locator('#a-1000').getAttribute('data-n'), '1000');
      console.log(JSON.stringify({ width, article86Visible: true, crossPart1000: true }));
      await page.close();
    }
  } finally { await browser.close(); }
})().catch(error => { console.error(error.stack); process.exitCode = 1; });
