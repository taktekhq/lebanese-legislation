import test from 'node:test';
import assert from 'node:assert/strict';
import {pages} from './pages.mjs';
import {createDrafts, makeExport} from './core.mjs';
const memoryStorage = () => {
  const values = new Map();
  return {getItem:key=>values.get(key)??null,setItem:(key,value)=>values.set(key,value)};
};
test('page switching keeps each text and self-reported origin separate, including after reload', () => {
  const storage = memoryStorage(), state = createDrafts(pages, storage);
  state.set(pages[0], {text:'نص\n[illegible]',origin:'human-reading'});
  assert.deepEqual(state.get(pages[1]),{text:'',origin:'unspecified'});
  state.set(pages[1], {text:'<script>text stays text</script>',origin:'machine-assisted'});
  assert.equal(state.get(pages[0]).text,'نص\n[illegible]');
  const restored = createDrafts(pages,storage);
  assert.equal(restored.get(pages[1]).origin,'machine-assisted');
  const copy = restored.get(pages[0]); copy.text='changed';
  assert.equal(restored.get(pages[0]).text,'نص\n[illegible]');
});
test('export identifies the exact source page and never claims submission, review or licence', () => {
  const draft = {text:'قراءة\n[uncertain: كلمة]',origin:'machine-assisted'};
  const result = makeExport(pages[2], draft, new Date('2026-10-30T00:00:00Z'));
  assert.deepEqual(result.document,{ruling_id:61180,sha256:pages[2].sha256});
  assert.equal(result.page,2); assert.equal(result.text,draft.text);
  assert.deepEqual(result.origin,{self_report:'machine-assisted'});
  assert.equal(result.license,null); assert.equal(result.submitted,false); assert.equal(result.reviewed,false);
  assert.equal(result.page_numbering,'one-based-physical-pdf');
  assert.throws(()=>makeExport(pages[0],{text:'  ',origin:'human-reading'}));
});
test('blocked storage keeps editable drafts in memory and reports that persistence failed', () => {
  const blocked = {getItem(){throw new Error('Blocked')},setItem(){throw new Error('Quota')}};
  const state = createDrafts(pages,blocked);
  assert.equal(state.storageOK,false);
  assert.equal(state.set(pages[0],{text:'محلي',origin:'unspecified'}),false);
  assert.equal(state.get(pages[0]).text,'محلي');
  assert.equal(makeExport(pages[0],state.get(pages[0])).submitted,false);
});
test('invalid and unknown saved state cannot supply provenance or executable fields', () => {
  const storage = {getItem:()=>JSON.stringify({version:1,drafts:{'59270:7':{text:42,origin:'reviewed',sha256:'fake'}}}),setItem(){}};
  const state = createDrafts(pages,storage);
  assert.equal(state.storageOK,false);
  assert.deepEqual(state.get(pages[0]),{text:'',origin:'unspecified'});
  assert.throws(()=>state.set({ruling_id:0,page:1},{text:'x',origin:'human-reading'}));
  assert.equal(createDrafts(pages,{getItem:()=>'{',setItem(){}}).storageOK,false);
});
