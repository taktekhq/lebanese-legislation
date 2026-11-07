import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { register } from 'node:module';

import { originFetch, sameSecret } from '../src/origin.js';
import { PH_TOKEN, phTarget } from '../src/phrelay.js';

register('./workers-loader.mjs', import.meta.url);
const { default: worker } = await import('../src/index.js');

const T = PH_TOKEN;
const OTHER = 'phc_SomeoneElsesProjectToken0123456789abcdefghij';

test('the relay pins the token ph.js uses', () => {
  const src = readFileSync(new URL('../../../internal/site/static/ph.js', import.meta.url), 'utf8');
  const init = /posthog\.init\("([^"]+)",\s*\{\s*api_host:\s*"\/ph"/.exec(src);
  assert.ok(init, 'ph.js calls posthog.init with api_host /ph');
  assert.equal(init[1], PH_TOKEN);
});

function stubFetch(t) {
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url, init = {}) => {
    calls.push({ url: String(url), init });
    return new Response('upstream', { headers: {
      'Content-Type': 'application/javascript', 'Set-Cookie': 'a=b',
      'Cross-Origin-Resource-Policy': 'cross-origin',
    } });
  });
  return calls;
}
const env = { SITE: { get: async () => null }, ORIGIN_TEST_KEY: 'secret' };

// Every address the site's PostHog integration requests (posthog-js 1.40x,
// loaded by the snippet in ph.js with api_host "/ph").
const allowed = [
  ['GET', '/ph/static/array.js', 'https://eu-assets.i.posthog.com/static/array.js'],
  ['GET', '/ph/static/1.405.2/recorder.js', 'https://eu-assets.i.posthog.com/static/1.405.2/recorder.js'],
  ['GET', '/ph/static/1.405.2/lazy-recorder.js', 'https://eu-assets.i.posthog.com/static/1.405.2/lazy-recorder.js'],
  ['GET', '/ph/static/1.405.2/web-vitals.js', 'https://eu-assets.i.posthog.com/static/1.405.2/web-vitals.js'],
  ['GET', '/ph/static/1.405.2/dead-clicks-autocapture.js', 'https://eu-assets.i.posthog.com/static/1.405.2/dead-clicks-autocapture.js'],
  ['GET', '/ph/static/1.405.2/exception-autocapture.js', 'https://eu-assets.i.posthog.com/static/1.405.2/exception-autocapture.js'],
  ['GET', '/ph/static/1.405.2/surveys.js', 'https://eu-assets.i.posthog.com/static/1.405.2/surveys.js'],
  ['GET', '/ph/static/recorder.js?v=1.200.0', 'https://eu-assets.i.posthog.com/static/recorder.js?v=1.200.0'],
  ['GET', `/ph/array/${T}/config`, `https://eu.i.posthog.com/array/${T}/config`],
  ['GET', `/ph/array/${T}/config.js`, `https://eu.i.posthog.com/array/${T}/config.js`],
  ['POST', '/ph/flags/?v=2&config=true', 'https://eu.i.posthog.com/flags/?v=2&config=true'],
  ['POST', '/ph/decide/?v=4', 'https://eu.i.posthog.com/decide/?v=4'],
  ['POST', '/ph/e/?ip=0&_=1&ver=1.405.2&compression=gzip-js', 'https://eu.i.posthog.com/e/?ip=0&_=1&ver=1.405.2&compression=gzip-js'],
  ['POST', '/ph/i/v0/e/?compression=gzip-js', 'https://eu.i.posthog.com/i/v0/e/?compression=gzip-js'],
  ['POST', '/ph/s/?compression=gzip-js&ip=0', 'https://eu.i.posthog.com/s/?compression=gzip-js&ip=0'],
  ['POST', '/ph/batch/', 'https://eu.i.posthog.com/batch/'],
  ['POST', '/ph/i/v1/logs', 'https://eu.i.posthog.com/i/v1/logs'],
  ['GET', `/ph/api/surveys/?token=${T}`, `https://eu.i.posthog.com/api/surveys/?token=${T}`],
  ['GET', `/ph/api/early_access_features/?token=${T}`, `https://eu.i.posthog.com/api/early_access_features/?token=${T}`],
  ['GET', `/ph/api/web_experiments/?token=${T}`, `https://eu.i.posthog.com/api/web_experiments/?token=${T}`],
];
for (const [method, path, target] of allowed) {
  test(`${method} ${path} is relayed to ${target}`, async t => {
    const calls = stubFetch(t);
    const body = method === 'POST' ? 'data=1' : undefined;
    const res = await worker.fetch(new Request('https://lebaneselegislation.com' + path, { method, body }), env, { waitUntil() {} });
    assert.equal(res.status, 200);
    assert.equal(await res.text(), 'upstream');
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, target);
    assert.equal(calls[0].init.method, method);
    if (body) assert.equal(new TextDecoder().decode(calls[0].init.body), body);
  });
}

const refused = [
  `/ph/array/${OTHER}/config.js`, `/ph/array/${OTHER}/config`, `/ph/array/x/config.js`,
  `/ph/site_app/9/${OTHER}/h/`, `/ph/site_app/9/${T}/h/`,
  `/ph/api/surveys/?token=${OTHER}`, '/ph/api/surveys/', `/ph/flags/?v=2&token=${OTHER}`,
  `/ph/e/?api_key=${OTHER}`, `/ph/e/?x=${OTHER}`, `/ph/e/?x=%70hc_abc`, `/ph/static/x.js?t=${OTHER}`,
  '/ph/static/../array/x/config.js', '/ph/static/array.html', '/ph/static/a/b/c.js',
  '/ph/api/projects/', '/ph/', '/ph/x', '/ph/e/x', '/ph/array/', `/ph/array/${T}/other`,
  '/ph/i/v0/e/x', '/ph/capture/',
];
for (const path of refused) {
  test(`${path} is a 404 that never reaches PostHog`, async t => {
    const calls = stubFetch(t);
    const res = await originFetch(new Request('https://lebaneselegislation.com' + path), env, { waitUntil() {} });
    assert.equal(res.status, 404);
    assert.equal(res.headers.get('X-Content-Type-Options'), 'nosniff');
    assert.deepEqual(calls, []);
  });
}

test('phTarget refuses addresses outside /ph/', () => {
  assert.equal(phTarget(new URL('https://x/e/')), null);
});

test('only PostHog headers are forwarded, never cookies or our own', async t => {
  const calls = stubFetch(t);
  await originFetch(new Request('https://lebaneselegislation.com/ph/e/', {
    method: 'POST', body: '{}',
    headers: {
      'Content-Type': 'application/json', 'User-Agent': 'UA', Origin: 'https://lebaneselegislation.com',
      Cookie: 'll_ai=secret', Authorization: 'Bearer x', 'x-origin-test': 'secret', 'x-origin-side': 'green',
      'cf-connecting-ip': '203.0.113.9', 'x-forwarded-for': '198.51.100.1, 203.0.113.9', 'x-real-ip': '198.51.100.1',
      'cf-ray': 'abc', 'cf-ipcountry': 'LB',
    },
  }), env, { waitUntil() {} });
  const sent = Object.fromEntries(calls[0].init.headers);
  assert.deepEqual(sent, {
    'content-type': 'application/json', 'user-agent': 'UA', origin: 'https://lebaneselegislation.com',
    'x-forwarded-for': '203.0.113.9',
  });
  assert.equal(calls[0].init.redirect, 'manual');
});

test('relayed responses get nosniff and same-origin CORP, and no cookies', async t => {
  stubFetch(t);
  const res = await originFetch(new Request('https://lebaneselegislation.com/ph/static/array.js'), env, { waitUntil() {} });
  assert.equal(res.headers.get('X-Content-Type-Options'), 'nosniff');
  assert.equal(res.headers.get('Cross-Origin-Resource-Policy'), 'same-origin');
  assert.equal(res.headers.get('Content-Type'), 'application/javascript');
  assert.equal(res.headers.get('Set-Cookie'), null);
});

test('sameSecret', () => {
  assert.equal(sameSecret('secret', 'secret'), true);
  assert.equal(sameSecret('secreT', 'secret'), false);
  assert.equal(sameSecret('secre', 'secret'), false);
  assert.equal(sameSecret('secretx', 'secret'), false);
  assert.equal(sameSecret('', 'secret'), false);
  assert.equal(sameSecret(null, 'secret'), false);
  assert.equal(sameSecret('', ''), false);
  assert.equal(sameSecret('x', undefined), false);
});
