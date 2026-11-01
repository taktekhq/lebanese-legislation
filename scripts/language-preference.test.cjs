const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function visit(url, lang, saved, blocked = false) {
  const listeners = {};
  const location = new URL(url);
  const state = { saved, redirect: null };
  location.replace = value => { state.redirect = value; };
  const context = {
    URL, location,
    window: { addEventListener(name, fn) { listeners[name] = fn; } },
    localStorage: {
      getItem() { if (blocked) throw Error('Storage blocked'); return state.saved; },
      setItem(key, value) { if (blocked) throw Error('Storage blocked'); state.saved = value; },
    },
    document: {
      documentElement: { lang },
      addEventListener(name, fn) { listeners[name] = fn; },
    },
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../internal/site/static/language.js'), 'utf8'), context);
  state.switchTo = (language, type = 'click') => {
    const link = { hreflang: language, href: '', getAttribute: () => language };
    listeners[type]({ type, button: type === 'auxclick' ? 1 : 0, target: { closest: () => link }, defaultPrevented: false });
    return link.href;
  };
  state.restore = () => listeners.pageshow({ persisted: true });
  return state;
}

test('only an explicit choice is remembered', () => {
  for (const saved of [null, '', 'fr', 'EN']) {
    assert.equal(visit('https://example.com/laws/', 'ar', saved).redirect, null);
  }
  assert.equal(visit('https://example.com/en/laws/', 'en', null).saved, null);
});

test('return visits use the saved language and retain path, query and fragment', () => {
  assert.equal(visit('https://example.com/law/12/?q=tax&page=2#a-3', 'ar', 'en').redirect,
    'https://example.com/en/law/12/?q=tax&page=2#a-3');
  assert.equal(visit('https://example.com/en/law/12/?q=tax#a-3', 'en', 'ar').redirect,
    'https://example.com/law/12/?q=tax#a-3');
  assert.equal(visit('https://example.com/', 'ar', 'en').redirect, 'https://example.com/en/');
  assert.equal(visit('https://example.com/en/', 'en', 'ar').redirect, 'https://example.com/');
});

test('matching language never loops', () => {
  assert.equal(visit('https://example.com/en/laws/', 'en', 'en').redirect, null);
  assert.equal(visit('https://example.com/laws/', 'ar', 'ar').redirect, null);
});

test('switching overrides the previous choice and preserves the current address', () => {
  const en = visit('https://example.com/en/search/?q=tax#results', 'en', 'en');
  assert.equal(en.switchTo('ar'), 'https://example.com/search/?q=tax#results');
  assert.equal(en.saved, 'ar');
  const ar = visit('https://example.com/search/?q=tax#results', 'ar', 'ar');
  assert.equal(ar.switchTo('en'), 'https://example.com/en/search/?q=tax#results');
  assert.equal(ar.saved, 'en');
});

test('blocked browser storage still allows language switching', () => {
  const state = visit('https://example.com/laws/', 'ar', 'en', true);
  assert.equal(state.redirect, null);
  assert.equal(state.switchTo('en'), 'https://example.com/en/laws/');
});

test('back/forward cache restores the latest choice', () => {
  const state = visit('https://example.com/laws/', 'ar', null);
  state.saved = 'en';
  state.restore();
  assert.equal(state.redirect, 'https://example.com/en/laws/');
});

test('middle-click also remembers the selected language', () => {
  const state = visit('https://example.com/laws/', 'ar', 'ar');
  assert.equal(state.switchTo('en', 'auxclick'), 'https://example.com/en/laws/');
  assert.equal(state.saved, 'en');
});
