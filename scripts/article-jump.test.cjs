// Run with node --test scripts/article-jump.test.cjs.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const vm = require('node:vm');
const code = readFileSync(join(__dirname, '../internal/site/static/legislation.js'), 'utf8');

function jump({ split = false, number = '86', articles = ['1', '86'] } = {}) {
  const input = { value: number, blur() {} }, hidden = { value: '1', blur() {} };
  const message = { hidden: true };
  const result = { submitted: false, hash: null, scrolled: null };
  const form = {
    querySelector(selector) {
      if (selector === '[data-goto-msg]') return message;
      if (selector === 'input') return split ? hidden : input;
      if (selector === 'input[name="n"]') return input;
      throw new Error('Unexpected selector: ' + selector);
    },
    hasAttribute() { return split; },
    addEventListener(event, handler) { this.handler = handler; },
    submit() { result.submitted = true; }
  };
  const elements = articles.map(n => ({
    id: 'a-' + n,
    getAttribute() { return n; },
    scrollIntoView() { result.scrolled = n; },
    removeAttribute() {}, setAttribute() {}
  }));
  const document = {
    addEventListener() {},
    querySelector(selector) { return selector === '[data-goto]' ? form : null; },
    querySelectorAll(selector) { return selector === '.art[data-n]' ? elements : []; }
  };
  vm.runInNewContext(code, { document, history: { replaceState(_, __, hash) { result.hash = hash; } } });
  form.handler({ preventDefault() {} });
  return { ...result, message };
}

test('split decree reads article 86, not the preceding hidden split flag', () => {
  const result = jump({ split: true });
  assert.equal(result.hash, '#a-86');
  assert.equal(result.scrolled, '86');
  assert.equal(result.submitted, false);
});

test('split decree submits to the server when article 86 is on another part', () => {
  const result = jump({ split: true, articles: ['1', '2'] });
  assert.equal(result.submitted, true);
  assert.equal(result.hash, null);
  assert.equal(result.scrolled, null);
});

test('unsplit text accepts Arabic-Indic article numbers', () => {
  assert.equal(jump({ number: '٨٦' }).hash, '#a-86');
});

test('missing article on an unsplit text shows a message without navigating', () => {
  const result = jump({ articles: ['1', '2'] });
  assert.equal(result.message.hidden, false);
  assert.match(result.message.textContent, /86/);
  assert.equal(result.hash, null);
  assert.equal(result.submitted, false);
});
