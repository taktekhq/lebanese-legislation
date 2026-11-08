import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { GA_ID, gtagRelay } from '../src/gtagrelay.js';
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
