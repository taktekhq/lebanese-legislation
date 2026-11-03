import test from 'node:test';
import assert from 'node:assert/strict';
import { register } from 'node:module';
import { createHash } from 'node:crypto';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

register('./workers-loader.mjs', import.meta.url);
const { default: worker } = await import('../src/index.js');
const { isParts, parseParts, prepareFrame, assemble } = await import('../src/layout.js');

// Pages are stored as their own parts and joined into the shared frame here
// (src/layout.js, internal/staticexport/edgelayout.go). What a reader gets
// must be the page the exporter rendered, byte for byte, and pages stored
// whole before the change must keep being served as they are.

const enc = new TextEncoder();
const sha = (b) => createHash('sha256').update(b).digest('hex');

// the module keeps the generation and the frame list for a minute: each
// scenario moves the clock past that
let clock = Date.now();
Date.now = () => clock;
const later = () => { clock += 10 * 60 * 1000; };

globalThis.caches = { default: { match: async () => undefined, put: async () => {} } };
const ctx = { waitUntil() {} };

function object(bytes, contentType, etag = '"' + sha(bytes).slice(0, 16) + '"') {
  return {
    body: bytes, httpEtag: etag, etag: etag.replaceAll('"', ''),
    httpMetadata: { contentType, cacheControl: 'public, max-age=300' },
    arrayBuffer: async () => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
    text: async () => new TextDecoder().decode(bytes),
    json: async () => JSON.parse(new TextDecoder().decode(bytes)),
  };
}

// bucket answers like the R2 binding from a map of key to [bytes, type].
function bucket(files) {
  const reads = [];
  return {
    reads,
    SITE: {
      get: async (key) => { reads.push(key); const f = files.get(key); return f ? object(f[0], f[1]) : null; },
      head: async (key) => (files.has(key) ? {} : null),
    },
  };
}

function partsPage(key, parts) {
  const head = enc.encode('LLP1 ' + key.replace('/', ' ') + ' ' + parts.map((p) => enc.encode(p).length).join(',') + '\n');
  const body = parts.map((p) => enc.encode(p));
  const out = new Uint8Array(head.length + body.reduce((n, p) => n + p.length, 0));
  let at = 0;
  for (const b of [head, ...body]) { out.set(b, at); at += b.length; }
  return out;
}

const frame = { lang: 'ar', schema: 's1', slots: ['page:title', 'page:content'], text: ['<!doctype html><title>', '</title><main>', '</main><footer>ذيل</footer>\n'] };
const frameBytes = enc.encode(JSON.stringify(frame));

test('the parts of a page and its frame make the page', () => {
  const page = partsPage('ar/s1', ['قانون "1" &amp; 2', '<p>نص</p>']);
  assert.ok(isParts(page));
  assert.ok(!isParts(enc.encode('<!doctype html>')));
  const got = assemble(prepareFrame(frame), parseParts(page));
  assert.equal(new TextDecoder().decode(got), '<!doctype html><title>قانون "1" &amp; 2</title><main><p>نص</p></main><footer>ذيل</footer>\n');
  assert.throws(() => assemble(prepareFrame({ ...frame, schema: 's2' }), parseParts(page)), /does not fit/);
  assert.throws(() => parseParts(page.subarray(0, page.length - 1)), /truncated/);
});

test('a page in parts is served joined, with an ETag that names the frame', async () => {
  later();
  const id = sha(frameBytes).slice(0, 24);
  const files = new Map([
    ['_export/layout.json', [enc.encode(JSON.stringify({ layouts: [{ key: 'ar/s1', id }] })), 'application/json']],
    ['_layout/' + id + '.json', [frameBytes, 'application/json']],
    ['laws/index.html', [partsPage('ar/s1', ['القوانين', 'list']), 'text/html; charset=utf-8']],
    ['old/index.html', [enc.encode('<!doctype html>whole page'), 'text/html; charset=utf-8']],
    ['sitemap.xml', [enc.encode('<urlset/>'), 'application/xml']],
  ]);
  const env = bucket(files);
  const res = await worker.fetch(new Request('https://lebaneselegislation.com/laws/'), env, ctx);
  assert.equal(res.status, 200);
  assert.equal(await res.text(), '<!doctype html><title>القوانين</title><main>list</main><footer>ذيل</footer>\n');
  assert.equal(res.headers.get('Content-Type'), 'text/html; charset=utf-8');
  const etag = res.headers.get('ETag');
  assert.match(etag, new RegExp('-' + id.slice(0, 12) + '"$'));
  const again = await worker.fetch(new Request('https://lebaneselegislation.com/laws/', { headers: { 'If-None-Match': etag } }), env, ctx);
  assert.equal(again.status, 304);
  // pages stored whole, from before the edge layout, are served as they are
  const old = await worker.fetch(new Request('https://lebaneselegislation.com/old/'), env, ctx);
  assert.equal(await old.text(), '<!doctype html>whole page');
  const xml = await worker.fetch(new Request('https://lebaneselegislation.com/sitemap.xml'), env, ctx);
  assert.equal(await xml.text(), '<urlset/>');
  // the frames are not pages
  for (const p of ['/_layout/' + id + '.json', '/_export/layout.json']) {
    assert.equal((await worker.fetch(new Request('https://lebaneselegislation.com' + p), env, ctx)).status, 404);
  }
});

test('a page in parts with no frame goes to the origin, never out as parts', async () => {
  later();
  const files = new Map([['laws/index.html', [partsPage('ar/unknown', ['a', 'b']), 'text/html; charset=utf-8']]]);
  const env = bucket(files);
  let origin = 0;
  env.ORIGIN_BLUE = env.ORIGIN_GREEN = { fetch: async () => { origin++; return new Response('from origin'); } };
  const saved = console.error;
  console.error = () => {};
  try {
    const res = await worker.fetch(new Request('https://lebaneselegislation.com/laws/'), env, ctx);
    assert.equal(await res.text(), 'from origin');
  } finally {
    console.error = saved;
  }
  assert.equal(origin, 1);
});

test('the frame list is part of the page cache key, and absent leaves it as before', async () => {
  later();
  const keys = [];
  globalThis.caches = { default: { match: async (k) => { keys.push(k.url); }, put: async () => {} } };
  const files = new Map([['_export/generation', [enc.encode('run-7\n'), 'text/plain']], ['laws/index.html', [enc.encode('<!doctype html>x'), 'text/html']]]);
  await worker.fetch(new Request('https://lebaneselegislation.com/laws/'), bucket(files), ctx);
  later();
  files.set('_export/layout.json', [enc.encode('{"layouts":[]}'), 'application/json']);
  await worker.fetch(new Request('https://lebaneselegislation.com/laws/'), bucket(files), ctx);
  globalThis.caches = { default: { match: async () => undefined, put: async () => {} } };
  assert.equal(keys[0], 'https://lebaneselegislation.com/laws/?__export=run-7');
  assert.match(keys[1], /^https:\/\/lebaneselegislation\.com\/laws\/\?__export=run-7&__layout=[0-9a-f]+$/);
});

test('the exporter can ask whether this Worker joins pages', async () => {
  const res = await worker.fetch(new Request('https://lebaneselegislation.com/_build/layout'), bucket(new Map()), ctx);
  assert.equal(res.status, 200);
  assert.match(await res.text(), /^LLP1/);
});

// EDGE_LAYOUT_DIR holds two exports of the same paths: whole/ as pages were
// stored before (or by the exporter of the main branch), parts/ in parts
// with its frames (.export/_layout, .export/layout.json). Every HTML page of
// whole/ is asked of the Worker over a bucket made of parts/ and must come
// back byte for byte. cmd/static-export's TestEdgeLayoutThroughWorker runs
// this on the pages that need no database; docs/static-export.md has the
// run over a sample of the corpus.
const dir = process.env.EDGE_LAYOUT_DIR;
test('exported pages come back byte for byte through the Worker', { skip: !dir && 'EDGE_LAYOUT_DIR not set' }, async () => {
  later();
  const manifest = (d) => readFileSync(join(d, '.export', 'manifest.jsonl'), 'utf8').trim().split('\n').filter(Boolean).map((l) => JSON.parse(l));
  const whole = manifest(join(dir, 'whole'));
  const parts = new Map(manifest(join(dir, 'parts')).filter((e) => e.f).map((e) => [e.f, e]));
  const files = new Map();
  const pdir = join(dir, 'parts');
  files.set('_export/layout.json', [readFileSync(join(pdir, '.export', 'layout.json')), 'application/json']);
  for (const f of readdirSync(join(pdir, '.export', '_layout'))) {
    files.set('_layout/' + f, [readFileSync(join(pdir, '.export', '_layout', f)), 'application/json']);
  }
  const env = bucket(new Map());
  env.SITE.get = async (key) => {
    const f = files.get(key);
    if (f) return object(new Uint8Array(f[0]), f[1]);
    const e = parts.get(key);
    if (!e || !existsSync(join(pdir, key))) return null;
    return object(new Uint8Array(readFileSync(join(pdir, key))), e.t);
  };
  env.ORIGIN_BLUE = env.ORIGIN_GREEN = { fetch: async () => new Response('origin', { status: 599 }) };
  let same = 0, inParts = 0;
  const kinds = new Map();
  const bad = [];
  for (const e of whole) {
    if (e.s !== 200 || !(e.t || '').startsWith('text/html')) continue;
    const want = readFileSync(join(dir, 'whole', e.f));
    const res = await worker.fetch(new Request('https://lebaneselegislation.com' + e.p), env, ctx);
    const got = Buffer.from(await res.arrayBuffer());
    if (res.status === 200 && got.equals(want)) same++;
    else bad.push(e.p);
    if (parts.get(e.f)?.l) {
      inParts++;
      const lang = e.p.startsWith('/en/') ? 'en' : 'ar';
      const kind = lang + ':' + (e.p.replace(/^\/en/, '').split('/')[1] || 'home');
      kinds.set(kind, (kinds.get(kind) || 0) + 1);
    }
  }
  console.log(`edge layout: ${same} of ${same + bad.length} HTML pages identical through the Worker, ${inParts} of them stored in parts`);
  console.log('in parts by kind: ' + [...kinds].sort().map(([k, n]) => k + '=' + n).join(' '));
  assert.deepEqual(bad.slice(0, 20), []);
  assert.ok(inParts > 0, 'no page was stored in parts');
});
