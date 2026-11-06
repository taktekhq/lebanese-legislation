import test from 'node:test';
import assert from 'node:assert/strict';
import { register } from 'node:module';

register('./workers-loader.mjs', import.meta.url);
const { default: worker } = await import('../src/index.js');

// The MCP Registry lists /assets/mark.png and /assets/favicon.svg as the
// server's icons. Directory sites and MCP clients show them on their own
// pages, which Cross-Origin-Resource-Policy: same-site blocks (6 Nov 2026).
const file = (type) => ({ body: 'x', httpEtag: '"i1"', uploaded: new Date('2026-11-06T12:00:00Z'), httpMetadata: { contentType: type } });
const env = { SITE: { get: async (key) => (key.startsWith('_') ? null : file(key.endsWith('.svg') ? 'image/svg+xml' : 'image/png')) } };
const ctx = { waitUntil() {} };
globalThis.caches = { default: { match: async () => undefined, put: async () => {} } };

test('the registry icons may be shown by other sites', async () => {
  for (const path of ['/assets/mark.png', '/assets/favicon.svg']) {
    const res = await worker.fetch(new Request('https://lebaneselegislation.com' + path), env, ctx);
    assert.equal(res.status, 200, path);
    assert.equal(res.headers.get('Cross-Origin-Resource-Policy'), 'cross-origin', path);
    assert.equal(res.headers.get('Access-Control-Allow-Origin'), '*', path);
  }
});

test('other files stay same-site', async () => {
  for (const path of ['/assets/og.png', '/laws/']) {
    const res = await worker.fetch(new Request('https://lebaneselegislation.com' + path), env, ctx);
    assert.equal(res.headers.get('Cross-Origin-Resource-Policy'), 'same-site', path);
    assert.equal(res.headers.get('Access-Control-Allow-Origin'), null, path);
  }
});
