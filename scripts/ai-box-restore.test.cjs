// Run with node --test scripts/ai-box-restore.test.cjs.
// A reader who opens a cited source and presses Back must get the answer they
// already had from sessionStorage, with no second POST /search/ai/ (each POST
// spends one of their daily questions). These tests load the search page
// twice in one tab against a fake page and count the POSTs.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const vm = require('node:vm');
const code = readFileSync(join(__dirname, '../internal/site/static/legislation.js'), 'utf8');

function el(extra = {}) {
  return {
    hidden: false, textContent: '', children: [],
    classList: { add() {}, remove() {}, toggle() { return false; }, contains() { return false; } },
    setAttribute() {}, removeAttribute() {}, getAttribute() { return null; },
    addEventListener() {}, append(c) { this.children.push(c); }, remove() {},
    querySelector() { return null; }, querySelectorAll() { return []; },
    ...extra
  };
}

function fakeStorage() {
  const m = new Map();
  return {
    m,
    getItem: k => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => { m.set(k, String(v)); }
  };
}

// load runs legislation.js on one search page load. reply is the server's
// HTML; storage is the tab's sessionStorage (shared across loads). It
// resolves with how many POSTs were sent and the events tracked.
async function load({ storage, reply = '<p class="ai-box__text">answer</p>', q = 'ما هي مهلة الطعن؟', lang = 'ar' }) {
  const attrs = { 'data-q': q, 'data-url': '/search/ai/', 'data-empty': '' };
  let html = '';
  const target = el({
    scrollHeight: 0, clientHeight: 0,
    querySelector(s) {
      if (s === '.ai-box__text' && html.includes('ai-box__text')) return el();
      if (s === '.ai-box__limit' && html.includes('ai-box__limit')) return el();
      return null;
    },
    querySelectorAll(s) { return html.includes('ai-box__text') && s === '.ai-box__src' ? [el(), el()] : []; }
  });
  const box = el();
  const section = el({
    getAttribute(name) { return attrs[name] ?? null; },
    querySelector(s) { return s === '.ai-box' ? box : s === '[data-ai-content]' ? target : null; }
  });
  const document = {
    cookie: '',
    documentElement: { lang },
    addEventListener() {},
    createElement() {
      const t = el({ content: el() });
      Object.defineProperty(t, 'innerHTML', { set(v) { html = v; } });
      return t;
    },
    querySelector(s) { return s === '[data-ai-answer]' ? section : null; },
    querySelectorAll() { return []; },
    getElementById() { return null; }
  };
  const captured = [];
  const posthog = { __loaded: true, get_distinct_id: () => 'id', capture: (n, p) => captured.push([n, p]) };
  let posts = 0;
  const fetch = () => {
    posts++;
    return Promise.resolve({ ok: true, text: () => Promise.resolve(reply) });
  };
  vm.runInNewContext(code, {
    document, window: { posthog, sessionStorage: storage }, fetch, URLSearchParams, AbortController,
    setTimeout: () => 0, clearTimeout() {}
  });
  await new Promise(r => setTimeout(r, 10));
  return { posts, events: captured };
}

test('Back to the search page shows the saved answer and sends no second question', async () => {
  const storage = fakeStorage();
  const first = await load({ storage });
  assert.equal(first.posts, 1);
  assert.deepEqual(first.events.map(e => e[0]), ['ai_answer_shown']);
  assert.equal(first.events[0][1].restored, undefined);

  const back = await load({ storage });
  assert.equal(back.posts, 0);
  assert.deepEqual(back.events.map(e => e[0]), ['ai_answer_shown']);
  assert.equal(back.events[0][1].restored, true);
  assert.equal(back.events[0][1].sources, 2);
});

test('another question or language asks the server', async () => {
  const storage = fakeStorage();
  await load({ storage });
  assert.equal((await load({ storage, q: 'ما هي مدة الإيجار؟' })).posts, 1);
  assert.equal((await load({ storage, lang: 'en' })).posts, 1);
});

test('a limit message is not saved: the next load asks again', async () => {
  const storage = fakeStorage();
  await load({ storage, reply: '<p class="ai-box__limit">limit</p>' });
  assert.equal(storage.m.size, 0);
  assert.equal((await load({ storage })).posts, 1);
});

test('storage that throws (private mode, blocked) still asks and shows the answer', async () => {
  const storage = { getItem() { throw new Error('denied'); }, setItem() { throw new Error('denied'); } };
  const r = await load({ storage });
  assert.equal(r.posts, 1);
  assert.deepEqual(r.events.map(e => e[0]), ['ai_answer_shown']);
});
