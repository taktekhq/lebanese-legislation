import { readFile } from 'node:fs/promises';
import test from 'node:test';
import assert from 'node:assert/strict';

const source = (await readFile(new URL('../src/index.js', import.meta.url), 'utf8'))
  .replace('import { Container } from "@cloudflare/containers";', `class Container {
    constructor() {}
    async fetch(request) { return new Response('origin'); }
  }`);
const { default: worker, Origin } = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));

test('all requests use the one instance named main; admin endpoints use log port or destroy', async () => {
  const calls = [];
  const env = { ORIGIN: {
    idFromName(name) { assert.equal(name, 'main'); return name; },
    get(id, options) {
      assert.equal(id, 'main');
      assert.equal(options.locationHint, 'weur');
      return {
        fetch: async request => { calls.push(request.url); return new Response('app'); },
        containerFetch: async (url, port) => { calls.push([url, port]); return new Response('log'); },
        destroy: async () => { calls.push('destroy'); },
      };
    },
  } };
  assert.equal(await (await worker.fetch(new Request('https://origin/healthz'), env)).text(), 'app');
  assert.equal(await (await worker.fetch(new Request('https://origin/_origin/log?side=green'), env)).text(), 'log');
  assert.equal(await (await worker.fetch(new Request('https://origin/_origin/restart?side=blue'), env)).text(), 'container stopped\n');
  await worker.fetch(new Request('https://origin/_origin/restart', { method: 'POST' }), env);
  assert.deepEqual(calls, ['https://origin/healthz', ['http://container/log', 8081], 'destroy', 'https://origin/_origin/restart']);
});

test('Origin forwards container env and waits for readiness once per start', async () => {
  const origin = new Origin({}, { GEMINI_API_KEY: 'key', POSTHOG_KEY: 'posthog', OTHER: 'excluded' });
  assert.deepEqual(origin.envVars, { GEMINI_API_KEY: 'key', POSTHOG_KEY: 'posthog' });
  assert.equal(origin.sleepAfter, '72h');
  let waits = 0;
  origin.startAndWaitForPorts = async options => {
    waits++;
    assert.deepEqual(options, { ports: [8080], cancellationOptions: {
      instanceGetTimeoutMS: 120_000, portReadyTimeoutMS: 660_000,
    } });
  };
  const request = new Request('https://origin/healthz');
  await origin.fetch(request);
  await origin.fetch(request);
  assert.equal(waits, 1);
  origin.onStop();
  assert.equal(origin.portReady, false);
  await origin.fetch(request);
  assert.equal(waits, 2);
});
