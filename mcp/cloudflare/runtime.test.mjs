import test from 'node:test';
import assert from 'node:assert/strict';
import { allowedPath, ready } from './runtime.mjs';

test('only MCP and health paths are public', () => {
  for (const path of ['/mcp', '/mcp/', '/healthz']) assert.equal(allowedPath(path), true);
  for (const path of ['/', '/api/v1/search/', '/mcp/extra']) assert.equal(allowedPath(path), false);
});

test('startup passes credentials at runtime and waits for readiness', async () => {
  let config, tries = 0, timeout;
  const container = {
    running: false, images: { base: 'test-image' },
    start(value) { config = value; },
    async setInactivityTimeout(value) { timeout = value; },
    getTcpPort(port) {
      assert.equal(port, 8080);
      return { async fetch() { return new Response('', { status: ++tries < 3 ? 503 : 200 }); } };
    },
  };
  await ready(container, { DATABASE_URL: 'test-only', PUBLIC_URL: 'https://example.org' }, async () => {});
  assert.equal(tries, 3);
  assert.equal(timeout, 60000);
  assert.equal(config.env.DATABASE_URL, 'test-only');
  assert.equal(config.env.APP_MODE, 'mcp');
  assert.equal(config.enableInternet, true);
  assert.equal(config.image, 'test-image');
});

test('missing configuration fails before starting and failure hides credentials', async () => {
  await assert.rejects(ready({}, {}, async () => {}), /DATABASE_URL secret is required/);
  let starts = 0;
  const container = {
    running: true,
    start() { starts++; },
    async setInactivityTimeout() {},
    getTcpPort() { return { async fetch() { throw new Error('postgres://private'); } }; },
  };
  await assert.rejects(ready(container, { DATABASE_URL: 'private' }, async () => {}), /^Error: Container did not become ready$/);
  assert.equal(starts, 0);
});
