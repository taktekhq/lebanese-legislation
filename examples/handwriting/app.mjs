import {pages} from './pages.mjs';
import {createDrafts, makeExport} from './core.mjs';
const el = id => document.getElementById(id);
const selector = el('page'), text = el('transcription'), origin = el('origin');
const download = el('download'), image = el('scan-image');
// Access can throw in private modes or embedded browsers.
const storage = {getItem: key => window.localStorage.getItem(key), setItem: (key,value) => window.localStorage.setItem(key,value)};
const drafts = createDrafts(pages, storage);
let current = pages[0];
function storageMessage() {
  el('storage-status').textContent = drafts.storageOK
    ? 'Drafts save in this browser only. Download a copy before leaving.'
    : 'Local saving is unavailable or a saved draft could not be read. Keep this tab open and download a copy.';
}
function update() {
  drafts.set(current, {text: text.value, origin: origin.value});
  storageMessage();
  download.disabled = !text.value.trim();
  el('export-status').textContent = '';
}
function selectPage(index) {
  current = pages[index];
  const draft = drafts.get(current);
  text.value = draft.text; origin.value = draft.origin;
  el('export-status').textContent = '';
  el('page-meta').textContent = `${current.year} · ${current.court}`;
  el('pdf-link').href = `https://lebaneselegislation.com/ruling-files/${current.ruling_id}.pdf`;
  el('full-image').href = current.image;
  image.hidden = true;
  image.alt = `Original scan, ruling ${current.ruling_id}, physical PDF page ${current.page}`;
  el('image-status').textContent = 'Loading scan…';
  image.onload = () => { image.hidden = false; el('image-status').textContent = 'Scan loaded. Select the image to open it at full size.'; };
  image.onerror = () => { image.hidden = true; el('image-status').textContent = 'The preview could not load. Open the original PDF below.'; };
  image.src = current.image;
  download.disabled = !draft.text.trim();
  storageMessage();
}
selector.replaceChildren(...pages.map((page,index) => {
  const option = document.createElement('option'); option.value = index;
  option.textContent = `Ruling ${page.ruling_id}, page ${page.page}`; return option;
}));
selector.disabled = text.disabled = origin.disabled = false;
selector.addEventListener('change', () => selectPage(Number(selector.value)));
text.addEventListener('input', update); origin.addEventListener('change', update);
download.addEventListener('click', () => {
  let url;
  try {
    const draft = makeExport(current, drafts.get(current));
    url = URL.createObjectURL(new Blob([JSON.stringify(draft,null,2)+'\n'], {type:'application/json'}));
    const link = document.createElement('a'); link.href = url;
    link.download = `ruling-${current.ruling_id}-page-${current.page}-draft.json`;
    document.body.append(link); link.click(); link.remove();
    el('export-status').textContent = 'Draft download requested. Nothing was submitted.';
  } catch {
    el('export-status').textContent = 'The download could not start. Copy your text before leaving this page.';
  } finally { if (url) setTimeout(() => URL.revokeObjectURL(url),10000); }
});
selectPage(0);
