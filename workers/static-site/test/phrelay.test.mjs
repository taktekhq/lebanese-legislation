import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { register } from 'node:module';

import { originFetch, sameSecret } from '../src/origin.js';
import { PH_TOKEN, phTarget, botNetwork, botVisitor } from '../src/phrelay.js';
import { GA_ID } from '../src/gtagrelay.js';

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

// The JavaScript crawler on Huawei and Tencent Cloud (Nov 2026) must not reach
// PostHog: not the library, not events. Real visitors still do.
function withCf(path, cf, method = 'GET') {
  const req = new Request('https://lebaneselegislation.com' + path, { method, body: method === 'POST' ? 'data=1' : undefined });
  Object.defineProperty(req, 'cf', { value: cf });
  return req;
}
for (const cf of [{ asn: 136907, country: 'HK' }, { asn: 132203, country: 'SG' }, { asn: 4811, country: 'CN' }]) {
  for (const [method, path] of [['GET', '/ph/static/array.js'], ['POST', '/ph/i/v0/e/?compression=gzip-js'], ['POST', '/ph/e/']]) {
    test(`${method} ${path} from AS${cf.asn} ${cf.country} is a 204 that never reaches PostHog`, async t => {
      const calls = stubFetch(t);
      const res = await originFetch(withCf(path, cf, method), env, { waitUntil() {} });
      assert.equal(res.status, 204);
      assert.equal(calls.length, 0);
    });
  }
}
test('a visitor from a Lebanese ISP is relayed', async t => {
  const calls = stubFetch(t);
  const res = await originFetch(withCf('/ph/i/v0/e/', { asn: 42003, country: 'LB' }, 'POST'), env, { waitUntil() {} });
  assert.equal(res.status, 200);
  assert.equal(calls.length, 1);
});
test('botNetwork without a cf object is false', () => {
  assert.equal(botNetwork(undefined), false);
  assert.equal(botNetwork({ asn: 16509, country: 'US' }), false);
});

// The same crawler from home addresses: only its Chinese browser gives it away.
function withLang(path, lang, method = 'GET') {
  const req = new Request('https://lebaneselegislation.com' + path, {
    method, headers: { 'Accept-Language': lang }, body: method === 'POST' ? 'data=1' : undefined,
  });
  Object.defineProperty(req, 'cf', { value: { asn: 3215, country: 'FR' } });
  return req;
}
for (const lang of ['zh-CN,zh;q=0.9', 'zh', 'ZH-tw', 'zh;q=1']) {
  for (const [method, path] of [['GET', '/ph/static/array.js'], ['POST', '/ph/i/v0/e/'], ['GET', `/gtag/js?id=${GA_ID}`]]) {
    test(`${method} ${path} with Accept-Language ${lang} is a 204 that never leaves the Worker`, async t => {
      const calls = stubFetch(t);
      const res = await originFetch(withLang(path, lang, method), env, { waitUntil() {} });
      assert.equal(res.status, 204);
      assert.equal(res.headers.get('Cache-Control'), 'no-store');
      assert.equal(calls.length, 0);
    });
  }
}
test('gtag.js from a Huawei Cloud address is a 204', async t => {
  const calls = stubFetch(t);
  const res = await originFetch(withCf(`/gtag/js?id=${GA_ID}`, { asn: 136907, country: 'HK' }), env, { waitUntil() {} });
  assert.equal(res.status, 204);
  assert.equal(calls.length, 0);
});
for (const lang of ['ar-LB,ar;q=0.9,en;q=0.8', 'en-US,en;q=0.9,zh-CN;q=0.5', 'fr-FR', '']) {
  test(`Accept-Language "${lang}" from a French ISP still reaches PostHog and gtag.js`, async t => {
    const calls = stubFetch(t);
    assert.equal(botVisitor(withLang('/ph/e/', lang)), false);
    const ph = await originFetch(withLang('/ph/i/v0/e/', lang, 'POST'), env, { waitUntil() {} });
    assert.equal(ph.status, 200);
    const ga = await originFetch(withLang(`/gtag/js?id=${GA_ID}`, lang), env, { waitUntil() {} });
    assert.equal(ga.status, 200);
    assert.equal(calls.length, 2);
  });
}
