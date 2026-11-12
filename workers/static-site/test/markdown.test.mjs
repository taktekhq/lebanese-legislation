import test from 'node:test';
import assert from 'node:assert/strict';
import { register } from 'node:module';

import { toMarkdown, decodeEntities } from '../src/markdown.js';
import { AGENT_LINKS } from '../src/headers.js';

register('./workers-loader.mjs', import.meta.url);
const { default: worker } = await import('../src/index.js');

const PAGE = `<!doctype html>
<html lang="en" dir="ltr">
<head>
<title>Art. 50, Labour Code &middot; Lebanese Legislation</title>
<meta name="description" content="Article 50 of the Labour Code &amp; its amendments.">
<link rel="canonical" href="https://lebaneselegislation.com/en/law/190374/article/50/">
<style>main{color:red}</style>
<script>if (a<b) document.write("<p>never</p>");</script>
</head>
<body>
<header class="nav"><a href="/en/">Lebanese Legislation</a><nav><a href="/en/laws/">Legislation</a></nav></header>
<main id="top">
<section>
  <a class="back" href="/en/law/190374/#a-50"><span aria-hidden="true">&larr;</span> The whole text</a>
  <p class="kicker"><b lang="ar">قانون العمل</b> <span class="status">No repeal recorded</span></p>
  <h1 lang="ar">المادة 50</h1>
  <p>Last amended by <a href="/en/law/243935/">قانون 9640</a> &middot; <time datetime="2026-09-26">26 September 2026</time></p>
</section>
<article class="art">
  <div class="art__head"><h3>المادة 50</h3><div><button type="button" data-cite="x">Cite</button></div></div>
  <div class="art__ar"><p>آ- يحق لكل من صاحب العمل والعامل ان يفسخ * العقد_المعقود [بينهما].</p><p>- اذا كان الفسخ صادرا<br>من قبل صاحب العمل.</p></div>
</article>
<ul><li><a href="/en/ruling/1/">Ruling 1</a> 29/04/2020</li><li>Second<ul><li>Nested</li></ul></li></ul>
<ol start="3"><li>Three</li><li>Four</li></ol>
<nav class="filters"><a href="?kind=law">Law</a><a href="?kind=decree">Decree</a></nav>
<dl><dt>Status</dt><dd>In force</dd></dl>
<table><thead><tr><th>Year</th><th>Texts</th></tr></thead><tbody><tr><td>2025</td><td>1 | 2</td></tr></tbody></table>
<pre><code class="language-sh">curl -G 'https://x/api?q=1&amp;y=2'
  --data &lt;x&gt;</code></pre>
<blockquote><p>Quoted</p><p>twice</p></blockquote>
<form action="/search/"><input name="q"><button>Search</button></form>
<p hidden>Not shown</p>
<figure><img src="/assets/mcp/claude.svg" alt="Steps in Claude"><figcaption>Figure 1</figcaption></figure>
</main>
<footer><div class="footer__legal footer__notice"><p lang="en">Information from published official texts, not legal advice.</p></div><p>Taktek, LLC</p></footer>
<script defer src="/main.js"></script>
</body></html>`;

test('a page becomes Markdown: its own words, in order, nothing a browser only needs', () => {
  const md = toMarkdown(PAGE, 'https://lebaneselegislation.com/en/law/190374/article/50/');
  assert.equal(md, `---
title: "Art. 50, Labour Code · Lebanese Legislation"
description: "Article 50 of the Labour Code & its amendments."
url: https://lebaneselegislation.com/en/law/190374/article/50/
lang: en
---

[The whole text](https://lebaneselegislation.com/en/law/190374/#a-50)

**قانون العمل** No repeal recorded

# المادة 50

Last amended by [قانون 9640](https://lebaneselegislation.com/en/law/243935/) · 26 September 2026

### المادة 50

آ- يحق لكل من صاحب العمل والعامل ان يفسخ * العقد_المعقود [بينهما].

- اذا كان الفسخ صادرا  
من قبل صاحب العمل.

- [Ruling 1](https://lebaneselegislation.com/en/ruling/1/) 29/04/2020
- Second
  - Nested

3. Three
4. Four

[Law](https://lebaneselegislation.com/en/law/190374/article/50/?kind=law) [Decree](https://lebaneselegislation.com/en/law/190374/article/50/?kind=decree)

**Status**

In force

| Year | Texts |
| --- | --- |
| 2025 | 1 \\| 2 |

\`\`\`sh
curl -G 'https://x/api?q=1&y=2'
  --data <x>
\`\`\`

> Quoted
>
> twice

![Steps in Claude](https://lebaneselegislation.com/assets/mcp/claude.svg)

Figure 1

---

Information from published official texts, not legal advice.
`);
});

test('legal text is never altered: no character is escaped or dropped', () => {
  const text = 'المادة 12-1: * _ [ ] ( ) # > ` | \\ ⟦أ | ب⟧ 50% & < >';
  const html = `<html><body><main><p>${text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')}</p></main></body></html>`;
  assert.ok(toMarkdown(html, 'https://x/').includes('\n\n' + text + '\n'));
});

const body = (html) => toMarkdown('<html><body><main>' + html + '</main></body></html>', 'https://x.test/').split('---\n\n')[1];

test('adjacent elements are not pulled apart: 46/2021 stays 46/2021', () => {
  assert.equal(body('<p><bdi>46</bdi><bdi>/2021</bdi> <span>ال</span><span>مادة</span></p>'), '46/2021 المادة\n');
  // only links in a row, and a card's title and its line, get a space
  assert.equal(body('<p><a href="/a/">One</a><a href="/b/">Two</a></p>'), '[One](https://x.test/a/) [Two](https://x.test/b/)\n');
  assert.equal(body('<p><a href="/a/"><b>Title</b><span>12 articles</span></a></p>'), '[**Title** 12 articles](https://x.test/a/)\n');
  assert.equal(body('<p><b>المادة</b><span>5</span></p>'), '**المادة**5\n');
});

test('no-break spaces and U+FEFF are kept as they are', () => {
  const text = 'Art.\u00a05 «\u00a0texte\u00a0» 1\u202f000 a\ufeffb';
  assert.equal(body('<p>  Art.&nbsp;5 «&nbsp;texte&nbsp;»\n 1\u202f000   a\ufeffb </p>'), text + '\n');
  assert.equal(body('<p><b>\u00a0x\u00a0</b></p>'), '**\u00a0x\u00a0**\n');
});

test('text after a script or style is not lost when the page has an İ before it', () => {
  const md = toMarkdown('<html><head><title>İİİİİİİİİİİİİİİİ</title><style>p{}</style></head><body><main><p>' + 'İ'.repeat(40) +
    '</p><script>var a = "</p>";</script><p>after script</p><style>x{}</style><p>after style</p></main></body></html>', 'https://x.test/');
  assert.match(md, /after script\n\nafter style\n$/);
  assert.ok(!md.includes('var a'));
});

test('two readings of a scanned word are both kept, in the brackets the API uses', () => {
  assert.equal(body('<p>مبلغ <mark class="unsure" title="قراءتان">١٠٠<span class="unsure__or"> / </span>١٥٠</mark> ليرة</p>'), 'مبلغ ⟦١٠٠ | ١٥٠⟧ ليرة\n');
  assert.equal(body('<p><del>100</del><ins>500</ins> m<sup>2</sup> H<sub>2</sub>O</p>'), '~~100~~500 m<sup>2</sup> H<sub>2</sub>O\n');
});

test('the closing of a law keeps its lines: the date and each signature', () => {
  assert.equal(body('<div class="lawend lawend--closing"><p>بيروت في 23 ايلول سنة 1946\n  الامضاء: بشارة خليل الخوري\n\nصدر عن رئيس الجمهورية</p></div>'),
    'بيروت في 23 ايلول سنة 1946  \nالامضاء: بشارة خليل الخوري  \nصدر عن رئيس الجمهورية\n');
  assert.equal(body('<div class="reading"><div class="reading__page"><p>سطر اول\nسطر ثان</p></div></div>'), 'سطر اول  \nسطر ثان\n');
  assert.equal(body('<figure class="topic__quote"><blockquote>المادة 50\nسطر ثان</blockquote></figure><p>بعد\nذلك</p>'), '> المادة 50  \n> سطر ثان\n\nبعد ذلك\n');
  assert.equal(body('<div class="art__ar"><p>سطر اول\nسطر ثان</p></div>'), 'سطر اول سطر ثان\n'); // elsewhere a line break is a space, as in the browser
});

test('a link around a heading and a paragraph stays a link', () => {
  assert.equal(body('<a href="/t/"><h3>Rent</h3><p>What the law says</p></a>'), '[Rent What the law says](https://x.test/t/)\n');
});

test('code that holds a fence gets a longer one', () => {
  assert.equal(body('<pre>a\n```\nb</pre>'), '````\na\n```\nb\n````\n');
});

test('an address with brackets or a space cannot end the link early', () => {
  const md = toMarkdown('<main><p><a href="/law/1/?x=(a)&amp;y=b c">Text</a></p></main>', 'https://x.test/');
  assert.ok(md.includes('[Text](https://x.test/law/1/?x=%28a%29&y=b%20c)'), md);
});

test('entities', () => {
  assert.equal(decodeEntities('&constructor;&toString;&__proto__;'), '&constructor;&toString;&__proto__;');
  assert.equal(decodeEntities('&amp;&lt;&gt;&#39;&#x41;&middot;&nbsp;&unknown;&#34;'), '&<>\'A· &unknown;"');
});

test('a page with no <main> still converts', () => {
  assert.match(toMarkdown('<p>Hello <b>there</b></p>', 'https://x/'), /Hello \*\*there\*\*/);
});

function site() {
  const keys = [];
  const puts = [];
  globalThis.caches = { default: { match: async key => { keys.push(key.url); }, put: async (key, res) => { puts.push([key.url, res.headers.get('Content-Type')]); } } };
  const env = { SITE: { get: async key => key === '_export/generation' ? { text: async () => 'run-1\n' }
    : key === '_export/layout.json' ? null
    : key === 'en/law/1/index.html' ? { body: PAGE, arrayBuffer: async () => new TextEncoder().encode(PAGE).buffer, httpEtag: '"e1"', uploaded: new Date('2026-11-01T00:00:00Z'), httpMetadata: { contentType: 'text/html; charset=utf-8' } }
    : key === 'llms.txt' ? { body: '# llms', arrayBuffer: async () => new TextEncoder().encode('# llms').buffer, httpEtag: '"e2"', httpMetadata: { contentType: 'text/markdown; charset=utf-8' } }
    : null } };
  return { env, keys, puts, ctx: { waitUntil() {} } };
}

test('a request that prefers Markdown gets the page as Markdown, under its own cache key', async () => {
  const { env, keys, puts, ctx } = site();
  const res = await worker.fetch(new Request('https://lebaneselegislation.com/en/law/1/', { headers: { Accept: 'text/markdown, text/html;q=0.9' } }), env, ctx);
  assert.equal(res.status, 200);
  assert.equal(res.headers.get('Content-Type'), 'text/markdown; charset=utf-8');
  assert.equal(res.headers.get('Vary'), 'Accept');
  assert.equal(res.headers.get('Link'), AGENT_LINKS);
  assert.equal(res.headers.get('ETag'), '"e1-md"');
  const body = await res.text();
  assert.match(body, /^---\ntitle: "Art\. 50, Labour Code · Lebanese Legislation"/);
  assert.match(body, /\n# المادة 50\n/);
  assert.equal(res.headers.get('X-Markdown-Tokens'), String(Math.ceil(body.length / 4)));
  assert.equal(new URL(keys[0]).searchParams.get('__markdown'), '1');
  assert.deepEqual(puts.map(p => p[1]), ['text/markdown; charset=utf-8']);

  const head = await worker.fetch(new Request('https://lebaneselegislation.com/en/law/1/', { method: 'HEAD', headers: { Accept: 'text/markdown' } }), env, ctx);
  assert.equal(head.headers.get('Content-Type'), 'text/markdown; charset=utf-8');
  assert.equal(await head.text(), '');

  const same = await worker.fetch(new Request('https://lebaneselegislation.com/en/law/1/', { headers: { Accept: 'text/markdown', 'If-None-Match': '"e1-md"' } }), env, ctx);
  assert.equal(same.status, 304);
});

// One rule decides (markdownFirst, markdownask.js): Markdown must be wanted
// more than HTML, or as much and named first.
for (const [accept, type] of [
  ['text/markdown', 'text/markdown'],
  ['text/markdown, text/html, */*', 'text/markdown'],
  ['text/plain, text/x-markdown;q=0.5', 'text/markdown'],
  ['text/html, text/markdown', 'text/html'],
  ['text/html, text/markdown;q=0.8', 'text/html'],
  ['text/markdown;q=0', 'text/html'],
  ['*/*', 'text/html'],
]) {
  test(`Accept: ${accept} gets ${type}`, async () => {
    const { env, ctx } = site();
    const res = await worker.fetch(new Request('https://lebaneselegislation.com/en/law/1/', { headers: { Accept: accept } }), env, ctx);
    assert.equal(res.headers.get('Content-Type'), type + '; charset=utf-8');
  });
}

test('a browser gets the HTML page, with the Link header and Vary: Accept', async () => {
  const { env, keys, ctx } = site();
  const res = await worker.fetch(new Request('https://lebaneselegislation.com/en/law/1/', { headers: { Accept: 'text/html,application/xhtml+xml,*/*;q=0.8' } }), env, ctx);
  assert.equal(res.headers.get('Content-Type'), 'text/html; charset=utf-8');
  assert.equal(res.headers.get('Vary'), 'Accept');
  assert.equal(res.headers.get('Link'), AGENT_LINKS);
  assert.equal(res.headers.get('ETag'), '"e1"');
  assert.equal(await res.text(), PAGE);
  assert.equal(new URL(keys[0]).searchParams.get('__markdown'), null);
});

test('a page the converter cannot read is answered as HTML, not as an error', async () => {
  const { env, ctx } = site();
  const deep = '<main>' + '<div>'.repeat(20000) + 'x' + '</div>'.repeat(20000) + '</main>';
  const get = env.SITE.get;
  env.SITE.get = async key => key === 'en/law/2/index.html'
    ? { body: deep, arrayBuffer: async () => new TextEncoder().encode(deep).buffer, httpEtag: '"e3"', httpMetadata: { contentType: 'text/html; charset=utf-8' } }
    : get(key);
  const error = console.error;
  console.error = () => {};
  try {
    const res = await worker.fetch(new Request('https://lebaneselegislation.com/en/law/2/', { headers: { Accept: 'text/markdown' } }), env, ctx);
    assert.equal(res.status, 200);
    assert.equal(res.headers.get('Content-Type'), 'text/html; charset=utf-8');
    assert.equal(res.headers.get('ETag'), '"e3"');
  } finally {
    console.error = error;
  }
});

test('a file that is not a page is served as it is, whatever is asked', async () => {
  const { env, keys, ctx } = site();
  const res = await worker.fetch(new Request('https://lebaneselegislation.com/llms.txt', { headers: { Accept: 'text/markdown' } }), env, ctx);
  assert.equal(await res.text(), '# llms');
  assert.equal(new URL(keys[0]).searchParams.get('__markdown'), null); // one cache entry, not two
  assert.equal(res.headers.get('Link'), null);
  assert.equal(res.headers.get('ETag'), '"e2"');
});

test('the A2A lookup reaches the origin: POST, OPTIONS and GET', async () => {
  for (const method of ['POST', 'OPTIONS', 'GET']) {
    const calls = [];
    const env = { SITE: { get: async () => ({ text: async () => 'blue' }) }, ORIGIN_BLUE: { fetch: async r => { calls.push(r.method + ' ' + new URL(r.url).pathname); return new Response('ok'); } } };
    const res = await worker.fetch(new Request('https://lebaneselegislation.com/a2a', { method, body: method === 'POST' ? '{}' : undefined }), env, { waitUntil() {} });
    assert.equal(res.status, 200);
    assert.deepEqual(calls, [method + ' /a2a']);
  }
});

test('discovery files are kept at the edge like API answers; a missing one is not', async () => {
  const { originFetch } = await import('../src/origin.js');
  const puts = [];
  globalThis.caches = { default: { match: async () => undefined, put: async key => { puts.push(new URL(key.url).pathname); } } };
  const env = { SITE: { get: async () => ({ text: async () => 'blue' }) },
    ORIGIN_BLUE: { fetch: async r => new URL(r.url).pathname.includes('oauth')
      ? new Response('not found', { status: 404, headers: { 'Cache-Control': 'public, max-age=86400' } })
      : new Response('{}', { headers: { 'Cache-Control': 'public, max-age=3600, s-maxage=86400' } }) } };
  const waits = [];
  const ctx = { waitUntil: p => waits.push(p) };
  await originFetch(new Request('https://lebaneselegislation.com/.well-known/api-catalog'), env, ctx);
  await originFetch(new Request('https://lebaneselegislation.com/.well-known/oauth-authorization-server'), env, ctx);
  await Promise.all(waits);
  assert.deepEqual(puts, ['/.well-known/api-catalog']);
});

// The origin renders what R2 does not hold: a search, a filtered list, any
// address with a query. isitagentready.com asks for the home page with a
// query of its own, so its Markdown check reads an origin page.
function originSite(answer) {
  const calls = [];
  globalThis.caches = { default: { match: async () => undefined, put: async () => {} } };
  const env = { SITE: { get: async key => key === '_build/live-side' ? { text: async () => 'blue' } : null },
    ORIGIN_BLUE: { fetch: async r => { calls.push(r.method + ' ' + new URL(r.url).pathname + new URL(r.url).search); return answer(r); } } };
  return { env, calls, ctx: { waitUntil() {} } };
}
const html = (body = PAGE, headers = {}) => new Response(body, { headers: { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'public, max-age=300', 'Content-Length': '999', ETag: '"o1"', Vary: 'Accept-Encoding', ...headers } });

test('a page the origin renders is converted too when Markdown is preferred', async () => {
  const { env, calls, ctx } = originSite(() => html());
  const res = await worker.fetch(new Request('https://lebaneselegislation.com/?probe=1', { headers: { Accept: 'text/markdown' } }), env, ctx);
  assert.equal(res.status, 200);
  assert.equal(res.headers.get('Content-Type'), 'text/markdown; charset=utf-8');
  assert.equal(res.headers.get('Vary'), 'Accept-Encoding, Accept');
  assert.equal(res.headers.get('ETag'), null);
  assert.equal(res.headers.get('Content-Length'), null);
  assert.equal(res.headers.get('Cache-Control'), 'public, max-age=300');
  assert.equal(res.headers.get('Strict-Transport-Security'), 'max-age=31536000; includeSubDomains');
  const body = await res.text();
  assert.match(body, /\n# المادة 50\n/);
  assert.equal(res.headers.get('X-Markdown-Tokens'), String(Math.ceil(body.length / 4)));
  assert.deepEqual(calls, ['GET /?probe=1']); // asked once
});

test('a page R2 does not hold is converted from the origin as well', async () => {
  const { env, calls, ctx } = originSite(() => html());
  const res = await worker.fetch(new Request('https://lebaneselegislation.com/en/law/9/', { headers: { Accept: 'text/markdown, text/html;q=0.9' } }), env, ctx);
  assert.equal(res.headers.get('Content-Type'), 'text/markdown; charset=utf-8');
  assert.deepEqual(calls, ['GET /en/law/9/']);
});

test('everything else from the origin passes through as it is', async () => {
  // a browser
  let site = originSite(() => html());
  let res = await worker.fetch(new Request('https://lebaneselegislation.com/en/search/?q=rent', { headers: { Accept: 'text/html,*/*;q=0.8' } }), site.env, site.ctx);
  assert.equal(res.headers.get('Content-Type'), 'text/html; charset=utf-8');
  assert.equal(res.headers.get('ETag'), '"o1"');
  assert.equal(await res.text(), PAGE);
  // JSON, whatever is asked
  site = originSite(() => new Response('{"q":"x"}', { headers: { 'Content-Type': 'application/json; charset=utf-8' } }));
  res = await worker.fetch(new Request('https://lebaneselegislation.com/api/v1/search/?q=x', { headers: { Accept: 'text/markdown' } }), site.env, site.ctx);
  assert.equal(await res.text(), '{"q":"x"}');
  // a missing page, an error, a redirect
  for (const status of [404, 503, 301]) {
    site = originSite(() => new Response(status === 301 ? null : '<main>x</main>', { status, headers: { 'Content-Type': 'text/html; charset=utf-8', Location: '/x/' } }));
    res = await worker.fetch(new Request('https://lebaneselegislation.com/en/search/?q=rent', { headers: { Accept: 'text/markdown' } }), site.env, site.ctx);
    assert.equal(res.status, status);
    assert.equal(res.headers.get('Content-Type'), 'text/html; charset=utf-8');
  }
  // a POST (the search's answer)
  site = originSite(() => html('<main>answer</main>'));
  res = await worker.fetch(new Request('https://lebaneselegislation.com/en/search/answer/', { method: 'POST', body: 'q=x', headers: { Accept: 'text/markdown' } }), site.env, site.ctx);
  assert.equal(await res.text(), '<main>answer</main>');
  // a HEAD
  site = originSite(() => html(null));
  res = await worker.fetch(new Request('https://lebaneselegislation.com/?probe=1', { method: 'HEAD', headers: { Accept: 'text/markdown' } }), site.env, site.ctx);
  assert.equal(res.headers.get('Content-Type'), 'text/html; charset=utf-8');
});

test('an origin page the converter cannot read is answered as HTML', async () => {
  const deep = '<main>' + '<div>'.repeat(20000) + 'x' + '</div>'.repeat(20000) + '</main>';
  const { env, ctx } = originSite(() => html(deep));
  const error = console.error;
  console.error = () => {};
  try {
    const res = await worker.fetch(new Request('https://lebaneselegislation.com/?probe=1', { headers: { Accept: 'text/markdown' } }), env, ctx);
    assert.equal(res.status, 200);
    assert.equal(res.headers.get('Content-Type'), 'text/html; charset=utf-8');
    assert.equal(await res.text(), deep);
  } finally {
    console.error = error;
  }
});
