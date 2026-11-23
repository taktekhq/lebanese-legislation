import test from 'node:test';
import assert from 'node:assert/strict';
import { register } from 'node:module';
import { originFetch, requestRoute } from '../src/origin.js';
register('./workers-loader.mjs', import.meta.url);
const { default: worker } = await import('../src/index.js');
const TIMING = 'app;dur=12.3, gate;dur=0.4, rpc;desc="tools/call", tool;desc="get_article", load;desc="0.42"';

async function run(t, { path = '/healthz', duration = 0, status = 200, hit = false, r2 = false, staticPage = false, serviceFail = false, probe = false, timing } = {}) {
  let now = 100_000;
  const events = [], pending = [];
  t.mock.method(Date, 'now', () => now);
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    if (String(url).includes('posthog')) events.push(String(url));
    return new Response('proxy');
  });
  const saved = globalThis.caches;
  globalThis.caches = { default: {
    match: async () => { now += hit ? duration : 500; return hit ? new Response('cached', { headers: timing ? { 'Server-Timing': timing } : {} }) : undefined; },
    put: async () => {},
  } };
  t.after(() => { globalThis.caches = saved; });
  const service = { fetch: async () => { now += duration; if (serviceFail) throw Error('offline'); return new Response('origin', { status, headers: timing ? { 'Server-Timing': timing } : {} }); } };
  const env = { ORIGIN_TEST_KEY: 'probe-key',
    SITE: { get: async key => key === '_export/generation' || key === '_export/layout.json' ? null : key === '_build/live-side' ? { text: async () => 'green' }
      : (r2 || staticPage) ? { body: 'file', httpEtag: '"x"' } : null },
    ORIGIN_BLUE: service, ORIGIN_GREEN: service };
  const request = new Request('https://example.com' + path, probe ? { headers: { 'x-origin-test': 'probe-key', 'x-origin-side': 'blue' } } : {});
  Object.defineProperty(request, 'cf', { value: { colo: 'BEY' } });
  const response = await (staticPage ? worker.fetch(request, env, { waitUntil: p => pending.push(p) })
    : originFetch(request, env, { waitUntil: p => pending.push(p) }));
  await Promise.all(pending);
  return { response, events };
}

for (const [path, route] of [
  ['/en/api/v1/laws/?cursor=abc', '/api/v1/laws/?'], ['/api/v1/law/244611/', '/api/v1/law/'], ['/api/v1/', '/api/v1/'],
  ['/mcp', '/mcp'], ['/en/laws/?after=x&kind=y', '/laws/?'], ['/ar/law/12/article/3/', '/law/'], ['/healthz', '/healthz'],
  ['/ul/uploads/images/1.png', '/ul/'], ['/en', '/'], ['/%D8%A7/', '/other/'], ['/search/?q=secret', '/search/?'],
]) {
  test(`route of ${path} is ${route}`, () => {
    assert.equal(requestRoute(new URL('https://example.com' + path)), route);
  });
}
// The Worker sends no analytics event about a request (PostHog removed
// 23 Nov 2026), whatever its kind, speed or status.
for (const options of [{ path: '/en/api/v1/search/?q=private', duration: 2000 }, { path: '/mcp' }, { path: '/en/search/answer/' },
  { status: 503 }, { status: 404 }, { path: '/api/v1/law/1/', hit: true }, { path: '/api/v1/search/?limit=1&q=law', probe: true },
  { path: '/ruling-files/9.pdf', r2: true }, { path: '/law/1/', staticPage: true }]) {
  test(`no events for ${JSON.stringify(options)}`, async t => {
    const { events } = await run(t, options);
    assert.deepEqual(events, []);
  });
}
test('a failing origin still answers 503 and sends no event', async t => {
  const { response, events } = await run(t, { serviceFail: true });
  assert.equal(response.status, 503);
  assert.deepEqual(events, []);
});
test('an edge cache hit is served without its stale Server-Timing', async t => {
  const { response, events } = await run(t, { path: '/en/api/v1/search/?q=x', hit: true, timing: TIMING });
  assert.equal(await response.text(), 'cached');
  assert.equal(response.headers.get('Server-Timing'), null);
  assert.deepEqual(events, []);
});
test('a miss passes the Server-Timing header on', async t => {
  const { response } = await run(t, { path: '/mcp', timing: TIMING });
  assert.equal(await response.text(), 'origin');
  assert.equal(response.headers.get('Server-Timing'), TIMING);
});
