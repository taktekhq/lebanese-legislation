export const origins = ['unspecified', 'human-reading', 'machine-assisted'];
const key = 'lebanese-legal-handwriting-drafts-v1';
export function createDrafts(pages, storage) {
  const drafts = new Map(pages.map(p => [String(p.ruling_id) + ':' + p.page, {text: '', origin: 'unspecified'}]));
  let storageOK = true;
  try {
    const raw = storage.getItem(key);
    if (raw !== null) {
      if (raw.length > 2000000) throw new Error('Oversized saved draft');
      const saved = JSON.parse(raw);
      if (saved.version !== 1 || !saved.drafts || typeof saved.drafts !== 'object') throw new Error('Unknown saved draft');
      for (const [id, value] of drafts) {
        const candidate = saved.drafts[id];
        if (!candidate) continue;
        if (typeof candidate.text !== 'string' || candidate.text.length > 100000 || !origins.includes(candidate.origin)) throw new Error('Invalid saved draft');
        drafts.set(id, {text: candidate.text, origin: candidate.origin});
      }
    }
  } catch { storageOK = false; }
  const idFor = page => String(page.ruling_id) + ':' + page.page;
  return {
    get: page => ({...drafts.get(idFor(page))}),
    get storageOK() { return storageOK; },
    set(page, value) {
      if (!drafts.has(idFor(page)) || typeof value.text !== 'string' || value.text.length > 100000 || !origins.includes(value.origin)) throw new Error('Invalid draft');
      drafts.set(idFor(page), {text: value.text, origin: value.origin});
      try {
        storage.setItem(key, JSON.stringify({version: 1, drafts: Object.fromEntries(drafts)}));
        storageOK = true;
      } catch { storageOK = false; }
      return storageOK;
    }
  };
}
export function makeExport(page, draft, now = new Date()) {
  if (typeof draft.text !== 'string' || !draft.text.trim() || !origins.includes(draft.origin)) throw new Error('Write a draft first.');
  return {
    format: 'lebanese-legal-handwriting-draft-v1',
    document: {ruling_id: page.ruling_id, sha256: page.sha256},
    page: page.page,
    page_numbering: 'one-based-physical-pdf',
    text: draft.text,
    origin: {self_report: draft.origin},
    license: null,
    submitted: false,
    reviewed: false,
    created_at: now.toISOString()
  };
}
