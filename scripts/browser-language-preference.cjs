// BASE_URL required. Optional PLAYWRIGHT_MODULE and CHROME_PATH.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.BASE_URL;
if (!base) throw Error('Set BASE_URL');

(async () => {
  const browser = await chromium.launch({ headless: true,
    executablePath: process.env.CHROME_PATH || undefined, args: ['--no-sandbox'] });
  try {
    for (const width of [390, 1440]) {
      const context = await browser.newContext({ viewport: { width, height: 1000 }, reducedMotion: 'reduce' });
      const page = await context.newPage();
      await page.goto(base + '/', { waitUntil: 'load' });
      assert.equal(await page.locator('.lang-note').count(), 1, 'English note is visible');
      assert.ok(await page.locator('.lang-note a').isVisible());
      const title = await page.locator('h1').boundingBox();
      assert.ok(title && title.y < 350, `title at ${title && title.y}px on ${width}px viewport`);
      await page.locator('.lang-note').evaluate(el => { el.style.display = 'none'; });
      const withoutNote = await page.locator('h1').boundingBox();
      assert.equal(title.y, withoutNote.y, 'note must not move the title');
      await page.locator('.lang-note').evaluate(el => { el.style.display = ''; });
      const note = await page.locator('.lang-note a').boundingBox();
      const nav = await page.locator('.nav').boundingBox();
      assert.ok(note.y >= nav.y + nav.height, 'note sits below navigation');
      const badge = await page.locator('.ls-hero .badge').boundingBox();
      assert.ok(note.y + note.height <= badge.y, 'note does not cover the hero content');
      await page.locator('.lang-note a').click();
      await page.waitForURL(base + '/en/');
      assert.equal(await page.evaluate(() => localStorage.getItem('lebleg-language')), 'en');
      await page.goto(base + '/topics/?q=tax#top', { waitUntil: 'load' });
      await page.waitForURL(base + '/en/topics/?q=tax#top');
      await page.locator('.nav .lang-switch').click();
      await page.waitForURL(base + '/topics/?q=tax#top');
      assert.equal(await page.evaluate(() => localStorage.getItem('lebleg-language')), 'ar');
      await page.goto(base + '/en/topics/?q=tax#top', { waitUntil: 'load' });
      await page.waitForURL(base + '/topics/?q=tax#top');
      const topicNote = await page.locator('.lang-note a').boundingBox();
      const topicBack = await page.locator('.page .back').boundingBox();
      assert.ok(topicNote.y + topicNote.height <= topicBack.y, 'note clears topic page content');
      // A new tab shares the persistent browser preference.
      const second = await context.newPage();
      await second.goto(base + '/en/', { waitUntil: 'load' });
      await second.waitForURL(base + '/');
      const lawURL = await second.locator('.codes a.code').first().getAttribute('href');
      await second.goto(new URL(lawURL, base).href, { waitUntil: 'load' });
      const lawNote = await second.locator('.lang-note a').boundingBox();
      const lawBack = await second.locator('.dochead .back').boundingBox();
      assert.ok(lawNote.y + lawNote.height <= lawBack.y, 'note clears document page content');
      console.log(`${width}px: title y=${Math.round(title.y)}, both language choices and redirects passed`);
      await context.close();
    }
    const context = await browser.newContext();
    await context.addInitScript(() => Object.defineProperty(window, 'localStorage', {
      get() { throw new DOMException('Storage blocked', 'SecurityError'); },
    }));
    const page = await context.newPage();
    await page.goto(base + '/topics/', { waitUntil: 'load' });
    await page.locator('.lang-note a').click();
    await page.waitForURL(base + '/en/topics/');
    console.log('blocked storage: language switch passed');
    await context.close();
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
