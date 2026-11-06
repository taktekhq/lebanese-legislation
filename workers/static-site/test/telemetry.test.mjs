import test from 'node:test';
import assert from 'node:assert/strict';
import { register } from 'node:module';
import { originFetch, requestRoute } from '../src/origin.js';
register('./workers-loader.mjs', import.meta.url);
const { default: worker } = await import('../src/index.js');

async function run(t, { path = '/healthz', duration = 0, random = 0.9, status = 200, hit = false, fail = false, r2 = false, staticPage = false, serviceFail = false } = {}) {
  let now = 100_000;
  const events = [], pending = [];
  t.mock.method(Date, 'now', () => now);
  t.mock.method(Math, 'random', () => random);
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    if (String(url).startsWith('https://eu.i.posthog.com/i/v0/e/')) {
      events.push(JSON.parse(options.body));
      if (fail) throw Error('PostHog offline');
    }
    return new Response('proxy');
  });
  const saved = globalThis.caches;
  globalThis.caches = { default: {
    match: async () => { now += hit ? duration : 500; return hit ? new Response('cached') : undefined; },
    put: async () => {},
  } };
  t.after(() => { globalThis.caches = saved; });
  const service = { fetch: async () => { now += duration; if (serviceFail) throw Error('offline'); return new Response('origin', { status }); } };
  const env = { POSTHOG_KEY: 'test-key',
    SITE: { get: async key => key === '_export/generation' || key === '_export/layout.json' ? null : key === '_build/live-side' ? { text: async () => 'green' }
      : (r2 || staticPage) ? { body: 'file', httpEtag: '"x"' } : null },
    ORIGIN_BLUE: service, ORIGIN_GREEN: service };
  const request = new Request('https://example.com' + path);
  Object.defineProperty(request, 'cf', { value: { colo: 'BEY' } });
  const response = await (staticPage ? worker.fetch(request, env, { waitUntil: p => pending.push(p) })
    : originFetch(request, env, { waitUntil: p => pending.push(p) }));
  await Promise.all(pending);
  return { response, events };
}

test('slow service requests send only anonymous header timing properties', async t => {
  const { response, events } = await run(t, { path: '/en/api/v1/search/?q=private', duration: 2000 });
  assert.equal(response.status, 200);
  assert.deepEqual(events, [{ api_key: 'test-key', event: 'origin_request', distinct_id: 'lebleg-origin', properties: {
    kind: 'api', route: '/api/v1/search/?', status: 200, duration_ms: 2000, side: 'green', cache: 'miss', colo: 'BEY', method: 'GET',
    $lib: 'lebleg-static-site-worker', sample_rate: 1,
  } }]);
});
for (const [random, count] of [[0.09, 1], [0.1, 0], [0.9, 0]]) {
  test(`fast request sampling at ${random}`, async t => {
    const { events } = await run(t, { random });
    assert.equal(events.length, count);
    if (count) assert.equal(events[0].properties.sample_rate, 0.1);
  });
}
for (const [path, kind] of [['/mcp', 'mcp'], ['/en/search/answer/', 'answer'], ['/ar/search/ai/deep/', 'answer'], ['/search/?q=law', 'search'], ['/scan/1/a.png', 'scan'], ['/search/', 'page']]) {
  test(`classify ${path}`, async t => {
    const { events } = await run(t, { path, random: 0 });
    assert.equal(events[0].properties.kind, kind);
    assert.equal(events[0].properties.sample_rate, ['mcp', 'answer'].includes(kind) ? 1 : 0.1);
  });
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
test('server errors are always sent', async t => {
  const { events } = await run(t, { status: 503 });
  assert.equal(events[0].properties.sample_rate, 1);
});
test('service exceptions report the returned 503', async t => {
  const { response, events } = await run(t, { serviceFail: true });
  assert.equal(response.status, 503);
  assert.equal(events[0].properties.status, 503);
});
test('edge cache hits are marked and timed', async t => {
  const { response, events } = await run(t, { path: '/api/v1/law/1/', hit: true, duration: 5, random: 0 });
  assert.equal(await response.text(), 'cached');
  assert.equal(events[0].properties.cache, 'hit');
  assert.equal(events[0].properties.duration_ms, 5);
});
for (const options of [{ path: '/ruling-files/9.pdf', r2: true }, { path: '/law/1/', staticPage: true }, { path: '/ph/e/' }]) {
  test(`no events for ${JSON.stringify(options)}`, async t => {
    const { events } = await run(t, options);
    assert.deepEqual(events, []);
  });
}
test('PostHog rejection cannot change the origin response', async t => {
  const { response, events } = await run(t, { duration: 2000, fail: true });
  assert.equal(await response.text(), 'origin');
  assert.equal(events.length, 1);
});
test('a pending PostHog request does not delay the response', async t => {
  t.mock.method(globalThis, 'fetch', () => new Promise(() => {}));
  const response = await originFetch(new Request('https://example.com/mcp'), {
    POSTHOG_KEY: 'test-key', SITE: { get: async () => null },
    ORIGIN_BLUE: { fetch: async () => new Response('ready') },
  }, { waitUntil() {} });
  assert.equal(await response.text(), 'ready');
});
