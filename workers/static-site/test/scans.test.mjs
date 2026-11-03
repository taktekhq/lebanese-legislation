import test from 'node:test';
import assert from 'node:assert/strict';
import { register } from 'node:module';

register('./workers-loader.mjs', import.meta.url);
const { default: worker } = await import('../src/index.js');

// Exercise the Worker entry point: a scan must use its mapped source, never
// expose arbitrary bucket files, and retain the container fallback on misses.
test('scans use the law map, cache it, and enforce the Go filename rule', async () => {
  const now = Date.now;
  let clock = now();
  Date.now = () => clock;
  const reads = [];
  const files = new Map([
    ['_build/scans/42/page-1.png', ['png', 'image/png']],
    ['_build/scans/42/page-001.png', ['zero-padded', 'image/png']],
    ['_build/scans/42/page-999.png', ['last', 'image/png']],
    ['_build/scans/42/source.pdf', ['pdf', 'application/pdf']],
    ['_build/scans/43/source.pdf', ['new pdf', 'application/pdf']],
  ]);
  let source = 42;
  const env = {
    SITE: { get: async key => {
      reads.push(key);
      if (key === '_export/scans.json') return { json: async () => ({ 2000000001: source }) };
      if (key === '_build/live-side') return null;
      const file = files.get(key);
      return file ? { body: file[0], httpMetadata: { contentType: file[1] } } : null;
    } },
    ORIGIN_BLUE: { fetch: async () => new Response('container', { status: 404 }) },
  };
  const fetch = (path, method = 'GET') => worker.fetch(new Request('https://lebaneselegislation.com' + path, { method }), env, {});
  try {
    for (const [file, body, type] of [
      ['page-1.png', 'png', 'image/png'],
      ['page-001.png', 'zero-padded', 'image/png'],
      ['page-999.png', 'last', 'image/png'],
      ['source.pdf', 'pdf', 'application/pdf'],
    ]) {
      for (const method of ['GET', 'HEAD']) {
        const res = await fetch(`/scan/2000000001/${file}`, method);
        assert.equal(res.status, 200);
        assert.equal(await res.text(), method === 'HEAD' ? '' : body);
        assert.equal(res.headers.get('Content-Type'), type);
        assert.equal(res.headers.get('Cache-Control'), 'public, max-age=86400');
        assert.equal(res.headers.get('X-Content-Type-Options'), 'nosniff');
      }
    }
    assert.equal(reads.filter(key => key === '_export/scans.json').length, 1);
    source = 43;
    clock += 10 * 60 * 1000 + 1;
    assert.equal(await (await fetch('/scan/2000000001/source.pdf')).text(), 'new pdf');
    assert.equal(reads.filter(key => key === '_export/scans.json').length, 2);

    for (const path of [
      '/scan/2000000099/source.pdf', '/scan/2000000001/page-2.png',
      '/scan/2000000001/page-1000.png', '/scan/2000000001/page-.png',
      '/scan/2000000001/source.pdf/extra', '/scan/2000000001/secret.txt',
      '/scan/2000000001/page-1Xpng', '/scan/2000000001/page-1.png%0A',
    ]) {
      const res = await fetch(path);
      assert.equal(res.status, 404, path);
      assert.equal(await res.text(), 'container', path);
    }
    const before = reads.length;
    for (const path of ['/_build/scans/42/source.pdf', '/_export/scans.json']) {
      assert.equal((await fetch(path)).status, 404);
    }
    assert.equal(reads.length, before);
    assert.equal((await fetch('/scan/2000000001/source.pdf', 'POST')).status, 405);

    clock += 10 * 60 * 1000 + 1;
    env.SITE.get = async () => null;
    assert.equal(await (await fetch('/scan/2000000001/source.pdf')).text(), 'container');
  } finally {
    Date.now = now;
  }
});
