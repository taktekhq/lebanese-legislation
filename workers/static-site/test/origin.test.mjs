import test from 'node:test';
import assert from 'node:assert/strict';

import { originFetch } from '../src/origin.js';
import worker from '../src/index.js';

function fixture(live = 'blue') {
  const calls = [];
  const service = side => ({
    fetch: async request => {
      const path = new URL(request.url).pathname;
      calls.push((path === '/_origin/restart' ? 'stop:' : path === '/_origin/log' ? 'log:' : '') + side);
      return new Response(side);
    },
  });
  const reads = [];
  const env = {
    SITE: { get: async key => { reads.push(key); return live === null ? null : { text: async () => live }; } },
    ORIGIN_BLUE: service('blue'), ORIGIN_GREEN: service('green'), ORIGIN_TEST_KEY: 'secret',
  };
  return { calls, reads, env };
}
const req = (path, headers = {}) => new Request('https://example.com' + path, { headers });

test('visitors use the R2 flag and cannot choose a side without the secret', async () => {
  for (const live of ['blue', 'green']) {
    const { env, calls } = fixture(live);
    await originFetch(req('/healthz', { 'x-origin-side': live === 'blue' ? 'green' : 'blue', 'x-origin-test': 'wrong' }), env);
    assert.deepEqual(calls, [live]);
  }
});
test('authenticated probes reach the chosen side and bypass API cache', async () => {
  const { env, calls } = fixture();
  globalThis.caches = { default: { match() { throw Error('must bypass cache'); } } };
  env.ORIGIN_MODE = 'hetzner';
  await originFetch(req('/api/v1/search/?q=law', { 'x-origin-test': 'secret', 'x-origin-side': 'green' }), env);
  assert.deepEqual(calls, ['green']);
});
test('admin endpoints select their side from the query', async () => {
  const { env, calls } = fixture();
  for (const path of ['/_origin/restart?side=green', '/_origin/log?side=blue']) {
    await originFetch(req(path, { 'x-origin-test': 'secret' }), env);
  }
  assert.deepEqual(calls, ['stop:green', 'log:blue']);
  assert.equal((await originFetch(req('/_origin/restart?side=red', { 'x-origin-test': 'secret' }), env)).status, 400);
});
test('Hetzner mode and AI bypass keep their existing behavior', async () => {
  const saved = globalThis.fetch;
  globalThis.fetch = async () => new Response('hetzner');
  try {
    const { env, calls } = fixture();
    env.ORIGIN_MODE = 'hetzner';
    assert.equal(await (await originFetch(req('/healthz'), env)).text(), 'hetzner');
    env.ORIGIN_MODE = 'container';
    env.AI_TO_HETZNER = '1';
    assert.equal(await (await originFetch(req('/search/answer/'), env)).text(), 'hetzner');
    assert.deepEqual(calls, []);
  } finally { globalThis.fetch = saved; }
});

test('authenticated probes bypass static pages as well as API caches', async () => {
  const { env, calls } = fixture();
  env.SITE = { get() { throw Error('must bypass R2'); } };
  globalThis.caches = { default: { match() { throw Error('must bypass cache'); } } };
  const response = await worker.fetch(req('/law/1/', { 'x-origin-test': 'secret', 'x-origin-side': 'green' }), env);
  assert.equal(response.status, 200);
  assert.deepEqual(calls, ['green']);
});

test('missing R2 flag defaults to blue and flag is cached for exactly 15 seconds', async () => {
  const { env, calls, reads } = fixture(null);
  const now = Date.now;
  let at = 100_000;
  Date.now = () => at;
  try {
    await originFetch(req('/healthz'), env);
    env.SITE.get = async key => { reads.push(key); return { text: async () => 'green\n' }; };
    at += 14_999;
    await originFetch(req('/healthz'), env);
    at += 1;
    await originFetch(req('/healthz'), env);
    assert.deepEqual(calls, ['blue', 'blue', 'green']);
    assert.deepEqual(reads, ['_build/live-side', '_build/live-side']);
  } finally { Date.now = now; }
});

test('admin endpoints require the test key before reaching either service', async () => {
  const { env, calls } = fixture();
  for (const path of ['/_origin/log?side=green', '/_origin/restart?side=blue']) {
    assert.equal((await originFetch(req(path), env)).status, 404);
  }
  assert.deepEqual(calls, []);
});

test('invalid authenticated side is rejected, and omitted side uses the flag', async () => {
  const { env, calls } = fixture('green');
  assert.equal((await originFetch(req('/healthz', { 'x-origin-test': 'secret', 'x-origin-side': 'red' }), env)).status, 400);
  await originFetch(req('/healthz', { 'x-origin-test': 'secret' }), env);
  assert.deepEqual(calls, ['green']);
});

test('R2 and service failures return 503, including admin requests', async () => {
  for (const path of ['/healthz', '/_origin/restart?side=green']) {
    const { env } = fixture();
    env.ORIGIN_GREEN.fetch = async () => { throw Error('offline'); };
    if (path === '/healthz') env.SITE.get = async () => { throw Error('offline'); };
    const response = await originFetch(req(path, { 'x-origin-test': 'secret' }), env);
    assert.equal(response.status, 503);
    assert.equal(response.headers.get('Retry-After'), '30');
  }
});

test('public API cache remains separated by side', async () => {
  const keys = [], pending = [];
  globalThis.caches = { default: {
    match: async key => { keys.push(key.url); }, put: async () => {},
  } };
  for (const live of ['blue', 'green']) {
    const { env, calls } = fixture(live);
    await originFetch(req('/api/v1/search/?q=law'), env, { waitUntil: p => pending.push(p) });
    assert.deepEqual(calls, [live]);
  }
  await Promise.all(pending);
  assert.deepEqual(keys.map(key => new URL(key).searchParams.get('__origin_side')), ['blue', 'green']);
});
