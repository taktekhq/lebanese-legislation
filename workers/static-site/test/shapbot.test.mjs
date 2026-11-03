import test from 'node:test';
import assert from 'node:assert/strict';
import { register } from 'node:module';

register('./workers-loader.mjs', import.meta.url);
const { default: worker } = await import('../src/index.js');

const SHAPBOT = 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ShapBot/0.1.0';

// ShapBot is served like any visitor: static pages, and search, the API and
// the MCP server, so the agents that use Parallel's index learn them.
test('ShapBot is not refused anywhere', async () => {
  globalThis.caches = { default: { match: async () => undefined, put: async () => {} } };
  const env = { SITE: { get: async key => key === '_export/generation'
    ? { text: async () => 'run-1\n' }
    : key === '_export/layout.json' ? null
    : { body: 'page', arrayBuffer: async () => new TextEncoder().encode('page').buffer, httpEtag: '"e"', httpMetadata: { contentType: 'text/html' } } } };
  const ctx = { waitUntil() {} };
  const get = path => worker.fetch(new Request('https://lebaneselegislation.com' + path, { headers: { 'User-Agent': SHAPBOT } }), env, ctx);
  assert.equal((await get('/laws/')).status, 200);
  assert.equal((await get('/en/topic/inheritance/')).status, 200);
  for (const p of ['/search/?q=tax', '/api/v1/laws', '/mcp']) {
    assert.notEqual((await get(p)).status, 429, p);
  }
});
