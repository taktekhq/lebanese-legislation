import test from 'node:test';
import assert from 'node:assert/strict';
import { register } from 'node:module';

register('./workers-loader.mjs', import.meta.url);
const { default: worker } = await import('../src/index.js');

const SHAPBOT = 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ShapBot/0.1.0';

// ShapBot reads the static pages like any crawler, and is kept off the origin.
test('ShapBot gets static pages and a 429 on origin routes', async () => {
  globalThis.caches = { default: { match: async () => undefined, put: async () => {} } };
  const env = { SITE: { get: async key => key === '_export/generation'
    ? { text: async () => 'run-1\n' }
    : key === '_export/layout.json' ? null
    : { body: 'page', arrayBuffer: async () => new TextEncoder().encode('page').buffer, httpEtag: '"e"', httpMetadata: { contentType: 'text/html' } } } };
  const ctx = { waitUntil() {} };
  const get = path => worker.fetch(new Request('https://lebaneselegislation.com' + path, { headers: { 'User-Agent': SHAPBOT } }), env, ctx);
  assert.equal((await get('/laws/')).status, 200);
  assert.equal((await get('/en/topic/inheritance/')).status, 200);
  assert.equal((await get('/search/?q=tax')).status, 429);
  assert.equal((await get('/api/v1/laws')).status, 429);
});
