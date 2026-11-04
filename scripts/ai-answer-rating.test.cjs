// Run with node --test scripts/ai-answer-rating.test.cjs.
// "Was this answer helpful?" under an AI answer (legislation.js) sends the
// rating, a one-tap reason or a note to PostHog only. These tests click the thumbs
// on a fake page and check what lands in PostHog's queue.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const vm = require('node:vm');
const code = readFileSync(join(__dirname, '../internal/site/static/legislation.js'), 'utf8');

function el(extra = {}) {
  return {
    hidden: false, textContent: '', children: [], on: {},
    classList: { add() {}, remove() {}, toggle() { return false; }, contains() { return false; } },
    setAttribute() {}, removeAttribute() {}, getAttribute() { return null; }, hasAttribute() { return false; },
    addEventListener(type, fn) { (this.on[type] = this.on[type] || []).push(fn); },
    append(c) { this.children.push(c); }, remove() {},
    querySelector() { return null; }, querySelectorAll() { return []; },
    ...extra
  };
}

// page answers a question on a fake search page and resolves with the box,
// the rating row the answer brought, and PostHog's queue.
async function page() {
  const attrs = { 'data-q': 'هل يحق للأجير بدل التدريب؟', 'data-url': '/search/ai/', 'data-empty': '' };
  const pressed = {};
  const thumb = (value) => el({
    getAttribute(n) { return n === 'data-rate' ? value : n === 'aria-pressed' ? String(!!pressed[value]) : null; },
    setAttribute(n, v) { if (n === 'aria-pressed') pressed[value] = v === 'true'; },
    closest(s) { return s === '[data-rate]' ? this : s === '[data-ai-rate]' ? rate : null; },
    focus() { focused = value; }
  });
  let focused = null;
  const textarea = { value: '', focus() {} };
  const why = el({ hidden: true });
  const form = el({ hidden: true, querySelector() { return textarea; }, closest(s) { return s === '[data-ai-note]' ? form : s === '[data-ai-rate]' ? rate : null; } });
  const expanded = {};
  const reason = (value) => el({
    getAttribute(n) { return n === 'data-reason' ? value : null; },
    setAttribute(n, v) { if (n === 'aria-expanded') expanded[value] = v; },
    closest(s) { return s === '[data-reason]' ? this : s === '[data-ai-rate]' ? rate : null; }
  });
  const reasons = { wrong_law: reason('wrong_law'), out_of_date: reason('out_of_date'), no_answer: reason('no_answer'), other: reason('other') };
  const thanks = el();
  let rated = false;
  const rateAttrs = { 'data-mode': 'quick', 'data-thanks': 'Thanks for rating this answer.', 'data-sent': 'Thanks, we got your note.', 'data-reason-sent': 'Thanks, that tells us what to fix.' };
  const up = thumb('up');
  const down = thumb('down');
  const rate = el({
    getAttribute(n) { return rateAttrs[n] ?? null; },
    setAttribute(n) { if (n === 'data-rated') rated = true; },
    hasAttribute(n) { return n === 'data-rated' && rated; },
    querySelectorAll(s) { return s === '[data-rate]' ? [up, down] : []; },
    querySelector(s) { return s === '[data-ai-why]' ? why : s === '[data-ai-note]' ? form : s === '[data-ai-thanks]' ? thanks : s === '[data-rate="down"]' ? down : null; }
  });
  let answered = false;
  const target = el({
    scrollHeight: 0, clientHeight: 0,
    querySelector(s) {
      if (!answered) return null;
      if (s === '.ai-box__text') return el({ innerText: 'Yes, the employer pays it back.' });
      if (s === '[data-ai-rate]') return rate;
      return null;
    },
    querySelectorAll(s) { return answered && s === '.ai-box__src' ? [el({ getAttribute: () => '/law/12/' })] : []; }
  });
  const box = el();
  const section = el({
    getAttribute(name) { return attrs[name] ?? null; },
    querySelector(s) { return s === '.ai-box' ? box : s === '[data-ai-content]' ? target : null; }
  });
  const document = {
    cookie: '',
    addEventListener() {},
    createElement() { return el({ content: el() }); },
    querySelector(s) { return s === '[data-ai-answer]' ? section : null; },
    querySelectorAll() { return []; },
    getElementById() { return null; }
  };
  // assets/load.js puts this queue on window.posthog until ph.js runs.
  const queue = [];
  queue.capture = function () { queue.push([...arguments]); };
  let done;
  const finished = new Promise(resolve => { done = resolve; });
  vm.runInNewContext(code, {
    document, window: { posthog: queue }, URLSearchParams, AbortController,
    matchMedia: () => ({ matches: false }),
    fetch: () => Promise.resolve({ ok: true, text: () => { answered = true; return Promise.resolve(''); } }),
    setTimeout: (fn, ms) => (ms >= 30000 ? 0 : setTimeout(fn, ms)),
    clearTimeout: () => done()
  });
  await finished;
  const fire = (type, target) => box.on[type].forEach(fn => fn({ target, preventDefault() {} }));
  return { box, rate, up, down, why, form, reasons, expanded, thanks, textarea, queue, fire, focused: () => focused };
}

test('the rating row moves below the box so a collapsed answer can be rated', async () => {
  const { box, rate } = await page();
  assert.ok(box.children.includes(rate));
});

test('thumbs down sends the rating with the question, answer and sources, then asks what was wrong', async () => {
  const { down, why, thanks, queue, fire } = await page();
  fire('click', down);
  const [name, props] = queue.find(c => c[0] === 'ai_answer_rated');
  assert.equal(name, 'ai_answer_rated');
  assert.equal(props.rating, 'down');
  assert.equal(props.changed, false);
  assert.equal(props.question, 'هل يحق للأجير بدل التدريب؟');
  assert.equal(props.mode, 'quick');
  assert.equal(props.answer, 'Yes, the employer pays it back.');
  assert.deepEqual([...props.sources], ['/law/12/']); // an array from the page's own realm
  assert.equal(why.hidden, false);
  assert.equal(thanks.textContent, '', 'the live thanks line stays empty while the reasons show');
});

test('one tap on a reason sends it and says thanks', async () => {
  const p = await page();
  p.fire('click', p.down);
  p.fire('click', p.reasons.out_of_date);
  const note = p.queue.find(c => c[0] === 'ai_answer_note');
  assert.equal(note[1].reason, 'out_of_date');
  assert.equal(note[1].note, '');
  assert.equal(note[1].rating, 'down');
  assert.equal(note[1].question, 'هل يحق للأجير بدل التدريب؟');
  assert.equal(p.why.hidden, true);
  assert.equal(p.thanks.textContent, 'Thanks, that tells us what to fix.');
  assert.equal(p.focused(), 'down', 'focus goes back to the thumb before the reasons go');
});

test('"Something else" opens the box; the note goes to PostHog, an empty one sends nothing', async () => {
  const p = await page();
  p.fire('click', p.down);
  p.fire('click', p.reasons.other);
  assert.equal(p.form.hidden, false);
  assert.equal(p.expanded.other, 'true');
  assert.equal(p.queue.some(c => c[0] === 'ai_answer_note'), false);
  p.textarea.value = '  It cited a repealed law.  ';
  p.fire('submit', p.form);
  const note = p.queue.find(c => c[0] === 'ai_answer_note');
  assert.equal(note[1].reason, 'other');
  assert.equal(note[1].note, 'It cited a repealed law.');
  assert.equal(p.thanks.textContent, 'Thanks, we got your note.');

  const q = await page();
  q.fire('click', q.down);
  q.fire('click', q.reasons.other);
  q.fire('submit', q.form);
  assert.equal(q.queue.some(c => c[0] === 'ai_answer_note'), false);
  assert.equal(q.thanks.textContent, 'Thanks for rating this answer.');
});

test('switching to thumbs up is sent again and marked changed; pressing the same thumb twice is not', async () => {
  const { up, down, queue, fire } = await page();
  fire('click', down);
  fire('click', up);
  fire('click', up);
  const ratings = queue.filter(c => c[0] === 'ai_answer_rated').map(c => [c[1].rating, c[1].changed]);
  assert.deepEqual(ratings, [['down', false], ['up', true]]);
});
