// Run with node --test scripts/ai-box-tracking.test.cjs.
// The AI answer box in legislation.js reports to PostHog, which loads after
// the page (assets/load.js). These tests run the box against a fake page and
// check what it sends with the question and what it tracks after the answer.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const vm = require('node:vm');
const code = readFileSync(join(__dirname, '../internal/site/static/legislation.js'), 'utf8');

const TOKEN = 'phc_pTpSY5wVsQrG8LN58z4zWXdMWjvqR6uPCY2eXV25sBV5';

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

// ask runs legislation.js on a search page with a question and resolves
// with the POST body it sent and the window.posthog it ended with.
async function ask({ posthog, cookie = '' }) {
  const attrs = { 'data-q': 'ما هي مهلة الطعن؟', 'data-url': '/search/ai/', 'data-empty': '' };
  let answered = false;
  const target = el({
    scrollHeight: 0, clientHeight: 0,
    querySelector(s) { return answered && s === '.ai-box__text' ? el() : null; },
    querySelectorAll(s) { return answered && s === '.ai-box__src' ? [el(), el()] : []; }
  });
  const box = el();
  const section = el({
    getAttribute(name) { return attrs[name] ?? null; },
    querySelector(s) { return s === '.ai-box' ? box : s === '[data-ai-content]' ? target : null; }
  });
  const document = {
    cookie,
    addEventListener() {},
    createElement() { return el({ content: el() }); },
    querySelector(s) { return s === '[data-ai-answer]' ? section : null; },
    querySelectorAll() { return []; },
    getElementById() { return null; }
  };
  let sent;
  let done;
  const finished = new Promise(resolve => { done = resolve; });
  const window = { posthog };
  const fetch = (url, opts) => {
    sent = new URLSearchParams(opts.body.toString());
    return Promise.resolve({ ok: true, text: () => { answered = true; return Promise.resolve('<p class="ai-box__text">answer</p>'); } });
  };
  vm.runInNewContext(code, {
    document, window, fetch, URLSearchParams, AbortController,
    setTimeout: (fn, ms) => (ms >= 30000 ? 0 : setTimeout(fn, ms)),
    clearTimeout: () => done()
  });
  await finished;
  return { sent, posthog: window.posthog };
}

test('before PostHog loads: the question carries the cookie ID and events wait in the queue', async () => {
  // assets/load.js puts this queue on window.posthog until ph.js runs.
  const queue = [];
  queue.capture = function () { queue.push(['capture', ...arguments]); };
  const cookie = 'theme=dark; ph_' + TOKEN + '_posthog=' + encodeURIComponent(JSON.stringify({ distinct_id: '01a1-visitor' }));
  const { sent } = await ask({ posthog: queue, cookie });
  assert.equal(sent.get('ph'), '01a1-visitor');
  assert.equal(sent.get('q'), 'ما هي مهلة الطعن؟');
  assert.deepEqual(queue.map(c => c[1]), ['ai_answer_shown']);
  assert.equal(queue[0][2].sources, 2);
});

test('once PostHog has loaded: its own ID goes with the question and events go straight to it', async () => {
  const captured = [];
  const posthog = { __loaded: true, get_distinct_id: () => 'loaded-id', capture: (name, props) => captured.push([name, props]) };
  const { sent } = await ask({ posthog, cookie: 'ph_' + TOKEN + '_posthog=' + encodeURIComponent('{"distinct_id":"older"}') });
  assert.equal(sent.get('ph'), 'loaded-id');
  assert.deepEqual(captured.map(c => c[0]), ['ai_answer_shown']);
});

test('a first page with no PostHog and no cookie still asks, without an ID', async () => {
  const { sent } = await ask({ posthog: undefined, cookie: 'ph_' + TOKEN + '_posthog=not-json' });
  assert.equal(sent.has('ph'), false);
  assert.equal(sent.get('q'), 'ما هي مهلة الطعن؟');
});
