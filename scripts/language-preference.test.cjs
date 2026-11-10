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
  for (const saved of [null, '', 'de', 'EN']) {
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

test('French is used on the pages that have a French version', () => {
  assert.equal(visit('https://example.com/about/', 'ar', 'fr').redirect, 'https://example.com/fr/about/');
  assert.equal(visit('https://example.com/en/laws/?kind=law#list', 'en', 'fr').redirect, 'https://example.com/fr/laws/?kind=law#list');
  assert.equal(visit('https://example.com/en/', 'en', 'fr').redirect, 'https://example.com/fr/');
  assert.equal(visit('https://example.com/fr/about/', 'fr', 'fr').redirect, null);
});

test('a page with no French version stays as it is for a French reader', () => {
  assert.equal(visit('https://example.com/en/law/12/', 'en', 'fr').redirect, null);
  assert.equal(visit('https://example.com/law/12/article/3/', 'ar', 'fr').redirect, null);
  const state = visit('https://example.com/en/law/12/', 'en', 'en');
  assert.equal(state.switchTo('fr'), '');
  assert.equal(state.saved, 'fr');
});

test('leaving French keeps the page', () => {
  assert.equal(visit('https://example.com/fr/about/', 'fr', 'en').redirect, 'https://example.com/en/about/');
  assert.equal(visit('https://example.com/fr/', 'fr', 'ar').redirect, 'https://example.com/');
  const fr = visit('https://example.com/fr/data/#license', 'fr', 'fr');
  assert.equal(fr.switchTo('ar'), 'https://example.com/data/#license');
  assert.equal(fr.switchTo('en'), 'https://example.com/en/data/#license');
  const en = visit('https://example.com/en/data/', 'en', 'en');
  assert.equal(en.switchTo('fr'), 'https://example.com/fr/data/');
});
