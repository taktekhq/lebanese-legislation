import test from 'node:test';
import assert from 'node:assert/strict';
import { register } from 'node:module';

register('./workers-loader.mjs', import.meta.url);
const { default: worker } = await import('../src/index.js');

// Pages sit in the edge cache for 12 hours (s-maxage), and CI cannot purge.
// On 2 Nov a full re-render that added the analytics tags stayed hidden behind
// the old copies until a manual purge. The cache key carries the export
// generation that the workflow writes last, so a finished build shows at once.
test('a new export generation gets new page cache keys', async () => {
  const keys = [];
  globalThis.caches = { default: { match: async key => { keys.push(key.url); }, put: async () => {} } };
  let generation = 'run-1';
  const env = { SITE: { get: async key => key === '_export/generation'
    ? { text: async () => generation + '\n' }
    : key === '_export/layout.json' ? null
    : { body: 'page', arrayBuffer: async () => new TextEncoder().encode('page').buffer, httpEtag: '"e"', httpMetadata: { contentType: 'text/html' } } } };
  const ctx = { waitUntil() {} };
  const now = Date.now;
  let clock = now();
  Date.now = () => clock;
  try {
    for (const g of ['run-1', 'run-2']) {
      generation = g;
      clock += 10 * 60 * 1000;
      const res = await worker.fetch(new Request('https://lebaneselegislation.com/laws/'), env, ctx);
      assert.equal(res.status, 200);
    }
  } finally {
    Date.now = now;
  }
  assert.deepEqual(keys.map(k => new URL(k).searchParams.get('__export')), ['run-1', 'run-2']);
});
