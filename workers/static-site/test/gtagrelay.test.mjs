import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { GA_ID, gtagRelay, botNetwork, botVisitor } from '../src/gtagrelay.js';
import { CSP } from '../src/headers.js';
import { route } from '../src/route.js';

test('the relay pins the measurement id load.js uses', () => {
  const src = readFileSync(new URL('../../../internal/site/static/assets/load.js', import.meta.url), 'utf8');
  assert.ok(src.includes(`add("/gtag/js?id=${GA_ID}", true)`));
  assert.ok(!src.includes('googletagmanager.com'));
});

test('the CSP no longer allows the Tag Manager host to serve scripts', () => {
  const script = /script-src ([^;]*)/.exec(CSP)[1];
  assert.ok(!script.includes('googletagmanager'));
});

test('/gtag/ goes to the origin Worker code', () => {
  assert.equal(route('GET', '/gtag/js', '?id=' + GA_ID), 'origin');
});

function stub(t, status = 200) {
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url) => {
    calls.push(String(url));
    return new Response('gtag()', { status, headers: { 'Content-Type': 'text/javascript', 'Set-Cookie': 'a=b' } });
  });
  return calls;
}
const req = (path) => new Request('https://lebaneselegislation.com' + path);

test('our id is fetched from Google and served same-origin', async (t) => {
  const calls = stub(t);
  const res = await gtagRelay(req(`/gtag/js?id=${GA_ID}`), {});
  assert.equal(res.status, 200);
  assert.deepEqual(calls, [`https://www.googletagmanager.com/gtag/js?id=${GA_ID}`]);
  assert.equal(res.headers.get('set-cookie'), null);
  assert.equal(res.headers.get('x-content-type-options'), 'nosniff');
  assert.match(res.headers.get('content-type'), /javascript/);
});

for (const path of ['/gtag/js?id=GTM-ABCDEF', '/gtag/js?id=G-OTHER', '/gtag/js', `/gtag/js?id=${GA_ID}&l=x`, '/gtag/destination?id=' + GA_ID, `/gtag/js/x?id=${GA_ID}`]) {
  test(`refused: ${path}`, async (t) => {
    const calls = stub(t);
    const res = await gtagRelay(req(path), {});
    assert.equal(res.status, 404);
    assert.equal(calls.length, 0);
  });
}

test('an upstream failure is a 502, not a cached page', async (t) => {
  stub(t, 500);
  const res = await gtagRelay(req(`/gtag/js?id=${GA_ID}`), {});
  assert.equal(res.status, 502);
});

// The crawler that runs JavaScript from cloud networks or with a Chinese
// browser gets no gtag.js, so GA4 never counts it.
function visitor(cf, lang) {
  const r = new Request(`https://lebaneselegislation.com/gtag/js?id=${GA_ID}`, { headers: lang ? { 'Accept-Language': lang } : {} });
  Object.defineProperty(r, 'cf', { value: cf });
  return r;
}
for (const [cf, lang] of [[{ asn: 136907, country: 'HK' }, ''], [{ asn: 4811, country: 'CN' }, ''], [{ asn: 3215, country: 'FR' }, 'zh-CN,zh;q=0.9']]) {
  test(`gtag.js for AS${cf.asn} ${cf.country} ${lang} is a 204 that never leaves the Worker`, async (t) => {
    const calls = stub(t);
    const res = await gtagRelay(visitor(cf, lang), {});
    assert.equal(res.status, 204);
    assert.equal(res.headers.get('Cache-Control'), 'no-store');
    assert.equal(calls.length, 0);
  });
}
test('real readers are not bots', () => {
  assert.equal(botNetwork(undefined), false);
  assert.equal(botNetwork({ asn: 16509, country: 'US' }), false);
  for (const lang of ['ar-LB,ar;q=0.9,en;q=0.8', 'en-US,en;q=0.9,zh-CN;q=0.5', 'fr-FR', '']) {
    assert.equal(botVisitor(visitor({ asn: 3215, country: 'FR' }, lang)), false, lang);
  }
});
