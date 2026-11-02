import { readFile } from 'node:fs/promises';
import test from 'node:test';
import assert from 'node:assert/strict';

// Exercise routing without a Workers runtime or starting containers.
const source = (await readFile(new URL('../src/origin.js', import.meta.url), 'utf8'))
  .replace('import { Container } from "@cloudflare/containers";', 'class Container {}')
  .replace('import { SECURITY_HEADERS } from "./headers.js";', 'const SECURITY_HEADERS = {};');
const { originFetch } = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));

function fixture(live = 'blue') {
  const calls = [];
  const ns = side => ({
    idFromName: name => name,
    get: () => ({
      fetch: async () => { calls.push(side); return new Response(side); },
      destroy: async () => { calls.push('stop:' + side); },
      containerFetch: async () => { calls.push('log:' + side); return new Response(side); },
    }),
  });
  return { calls, env: { LIVE: live, ORIGIN_BLUE: ns('blue'), ORIGIN_GREEN: ns('green'), ORIGIN_TEST_KEY: 'secret' } };
}
const req = (path, headers = {}) => new Request('https://example.com' + path, { headers });

test('visitors use LIVE and cannot choose a side without the secret', async () => {
  for (const live of ['blue', 'green']) {
    const { env, calls } = fixture(live);
    await originFetch(req('/healthz', { 'x-origin-side': live === 'blue' ? 'green' : 'blue' }), env);
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

const originURL = 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const indexSource = (await readFile(new URL('../src/index.js', import.meta.url), 'utf8'))
  .replace('"./route.js"', JSON.stringify(new URL('../src/route.js', import.meta.url).href))
  .replace('"./headers.js"', JSON.stringify(new URL('../src/headers.js', import.meta.url).href))
  .replace('"./origin.js"', JSON.stringify(originURL))
  .replace('export { Origin, OriginBlue, OriginGreen } from "./origin.js";', '');
const { default: worker } = await import('data:text/javascript;base64,' + Buffer.from(indexSource).toString('base64'));

test('authenticated probes bypass static pages as well as API caches', async () => {
  const { env, calls } = fixture();
  env.SITE = { get() { throw Error('must bypass R2'); } };
  globalThis.caches = { default: { match() { throw Error('must bypass cache'); } } };
  const response = await worker.fetch(req('/law/1/', { 'x-origin-test': 'secret', 'x-origin-side': 'green' }), env);
  assert.equal(response.status, 200);
  assert.deepEqual(calls, ['green']);
});
