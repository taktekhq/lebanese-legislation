import test from 'node:test';
import assert from 'node:assert/strict';
import { register } from 'node:module';

register('./workers-loader.mjs', import.meta.url);
const { default: worker, notModified } = await import('../src/index.js');

// Googlebot sends If-None-Match and If-Modified-Since. A 304 spares it the
// body of a page that did not change. Before, a page from the Worker's cache
// was answered whole whatever the request sent (audit, 4 Nov 2026).
const uploaded = new Date('2026-11-04T20:00:00Z');
const page = { body: 'page', arrayBuffer: async () => new TextEncoder().encode('page').buffer, httpEtag: '"e1"', uploaded, httpMetadata: { contentType: 'text/html' } };
const env = { SITE: { get: async key => key.startsWith('_export/') ? null : page } };
const ctx = { waitUntil() {} };

function fresh(stored) {
  globalThis.caches = { default: { match: async () => stored, put: async () => {} } };
}
const req = (headers = {}) => new Request('https://lebaneselegislation.com/laws/', { headers });

test('a page carries Last-Modified, the time it was uploaded', async () => {
  fresh(undefined);
  const res = await worker.fetch(req(), env, ctx);
  assert.equal(res.status, 200);
  assert.equal(res.headers.get('Last-Modified'), 'Wed, 04 Nov 2026 20:00:00 GMT');
  assert.equal(res.headers.get('ETag'), '"e1"');
});

test('a matching If-None-Match gets 304 from R2 and from the cache, W/ or not', async () => {
  fresh(undefined);
  assert.equal((await worker.fetch(req({ 'If-None-Match': '"e1"' }), env, ctx)).status, 304);
  const cached = new Response('page', { status: 200, headers: { ETag: '"e1"', 'Last-Modified': 'Wed, 04 Nov 2026 20:00:00 GMT', 'Cache-Control': 'public, max-age=300' } });
  fresh(cached);
  for (const inm of ['"e1"', 'W/"e1"', '"other", W/"e1"', '*']) {
    const res = await worker.fetch(req({ 'If-None-Match': inm }), env, ctx);
    assert.equal(res.status, 304, inm);
    assert.equal(await res.text(), '');
    assert.equal(res.headers.get('ETag'), '"e1"');
  }
  fresh(cached);
  assert.equal((await worker.fetch(req({ 'If-None-Match': '"e0"' }), env, ctx)).status, 200);
});

test('If-Modified-Since answers 304 only when the page is not newer, and If-None-Match wins', () => {
  const h = new Headers({ ETag: '"e1"', 'Last-Modified': 'Wed, 04 Nov 2026 20:00:00 GMT' });
  assert.equal(notModified(req({ 'If-Modified-Since': 'Wed, 04 Nov 2026 20:00:00 GMT' }), h), true);
  assert.equal(notModified(req({ 'If-Modified-Since': 'Thu, 05 Nov 2026 08:00:00 GMT' }), h), true);
  assert.equal(notModified(req({ 'If-Modified-Since': 'Tue, 03 Nov 2026 08:00:00 GMT' }), h), false);
  assert.equal(notModified(req({ 'If-Modified-Since': 'not a date' }), h), false);
  assert.equal(notModified(req({ 'If-None-Match': '"e0"', 'If-Modified-Since': 'Thu, 05 Nov 2026 08:00:00 GMT' }), h), false);
  assert.equal(notModified(req(), h), false);
});
