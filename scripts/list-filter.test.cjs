// Run with node --test scripts/list-filter.test.cjs.
// The filter box above a list (legislation.js): typing hides the titles on
// the page that do not match, Arabic spelling variants included, and sends
// nothing anywhere.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const vm = require('node:vm');
const code = readFileSync(join(__dirname, '../internal/site/static/legislation.js'), 'utf8');

function item(text) {
  const attrs = {};
  return { textContent: text, attrs, setAttribute(k, v) { attrs[k] = v; }, removeAttribute(k) { delete attrs[k]; } };
}

function page(titles) {
  const items = titles.map(item);
  let onInput = null;
  const input = { value: '', addEventListener(e, f) { if (e === 'input') onInput = f; } };
  const count = { textContent: '', getAttribute: (k) => ({ 'data-filter-none': 'none', 'data-filter-some': '%n of %t' })[k] };
  const box = {
    hidden: true,
    getAttribute: () => '.doclist > li',
    querySelector: (s) => (s === 'input' ? input : count),
  };
  const document = {
    querySelector() { return null; },
    querySelectorAll(s) { return s === '[data-filter]' ? [box] : s === '.doclist > li' ? items : []; },
    getElementById() { return null; },
    addEventListener() {}, documentElement: { lang: 'ar' }, body: {},
  };
  let fetched = 0;
  vm.runInNewContext(code, { document, window: {}, location: { search: '', pathname: '/' }, history: {}, localStorage: {},
    fetch: () => { fetched++; return Promise.resolve(); }, setTimeout, clearTimeout, URLSearchParams, matchMedia: () => ({ matches: false }) });
  return {
    box, count,
    type(v) { input.value = v; onInput(); return items.filter((i) => !('data-filtered-out' in i.attrs)).map((i) => i.textContent); },
    fetched: () => fetched,
  };
}

const titles = ['قانون الإيجارات', 'قانون العمل', 'مرسوم رقم ١٢٣ تنظيم المدرسة الرسمية', 'Code de procédure civile', 'نظام الموظفين', 'حماية الملكية الفنية والموسيقى'];

test('the box shows itself and filters the titles on the page', () => {
  const p = page(titles);
  assert.equal(p.box.hidden, false);
  assert.deepEqual(p.type('العمل'), ['قانون العمل']);
  assert.equal(p.count.textContent, '1 of 6');
  assert.deepEqual(p.type(''), titles);
  assert.equal(p.count.textContent, '');
});

test('Arabic variants: alef forms, ta marbuta and ha, ya and alef maqsura, diacritics', () => {
  const p = page(titles);
  assert.deepEqual(p.type('الايجارات'), ['قانون الإيجارات']);
  assert.deepEqual(p.type('المدرسه'), ['مرسوم رقم ١٢٣ تنظيم المدرسة الرسمية']);
  assert.deepEqual(p.type('قَانُون العَمَل'), ['قانون العمل']);
  assert.deepEqual(p.type('الموسيقي'), ['حماية الملكية الفنية والموسيقى']);
  assert.deepEqual(p.type('123'), ['مرسوم رقم ١٢٣ تنظيم المدرسة الرسمية']);
});

test('Latin accents and case are ignored; every word must match', () => {
  const p = page(titles);
  assert.deepEqual(p.type('PROCEDURE'), ['Code de procédure civile']);
  assert.deepEqual(p.type('قانون العمل'), ['قانون العمل']);
  assert.deepEqual(p.type('قانون غير'), []);
  assert.equal(p.count.textContent, 'none');
  assert.equal(p.fetched(), 0);
});
