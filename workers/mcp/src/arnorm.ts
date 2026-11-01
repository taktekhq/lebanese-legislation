// The TypeScript twin of internal/snapshot/arnorm.go and Postgres's
// ar_norm (db/schema.sql): drops tashkeel and tatweel, folds أ إ آ ٱ to ا,
// ى to ي, ة to ه. Used to normalize an incoming search query the same way
// the snapshot normalized text_ar/title before storing it in the FTS5
// tables, so one spelling of a word finds every other.

const AR_DIACRITICS = /[ً-ٰٟـ]/g;

const AR_FOLD: Record<string, string> = {
  "أ": "ا", "إ": "ا", "آ": "ا", "ٱ": "ا", "ى": "ي", "ة": "ه",
};

export function arNorm(s: string): string {
  if (!s) return s;
  s = s.replace(AR_DIACRITICS, "");
  let out = "";
  for (const ch of s) out += AR_FOLD[ch] ?? ch;
  return out;
}

// fts5Quote wraps a search term for an FTS5 MATCH query: double quotes make
// it a phrase/token match rather than FTS5 query syntax (so a user's "AND",
// "-word" etc. in the query text can't be mistaken for FTS5 operators), and
// embedded double quotes are doubled, mirroring SQL single-quote escaping.
export function fts5Quote(term: string): string {
  return '"' + term.replace(/"/g, '""') + '"';
}

// fts5MatchAll builds a MATCH expression requiring every word of q (each
// normalized and quoted) to appear, which is what the site's own search
// does for law titles (every word, folded, anywhere in the title).
export function fts5MatchAll(q: string): string {
  const words = arNorm(q).trim().split(/\s+/).filter(Boolean);
  return words.map(fts5Quote).join(" AND ");
}

// Little words that no search should be held up by, after arNorm.
export const AR_STOP = new Set(["في", "من", "على", "عن", "الى", "او", "ان", "التي", "الذي", "هل", "ما", "لدى", "رقم", "تاريخ", "بتاريخ", "و"]);

const STEM_PREFIXES = ["وال", "بال", "كال", "فال", "لل", "ال"];
const STEM_SUFFIXES = ["ات", "ين", "ون", "ه"];

// lightStem is a word (after arNorm) less a leading article ("ال", "وال",
// "بال", "لل"…) and a feminine or plural ending, when three letters are left:
// "الصوريه" is "صوري", "المحاكمات" is "محاكم", "والعقود" is "عقود".
export function lightStem(w: string): string {
  let s = w;
  for (const p of STEM_PREFIXES) {
    if (s.startsWith(p) && [...s].length - [...p].length >= 3) {
      s = s.slice(p.length);
      break;
    }
  }
  for (const x of STEM_SUFFIXES) {
    if (s.endsWith(x) && [...s].length - [...x].length >= 3) {
      s = s.slice(0, -x.length);
      break;
    }
  }
  return s;
}

const FORM_PREFIXES = ["", "ال", "وال", "بال", "لل", "و", "ب", "ل", "ف"];

// fts5Forms builds a MATCH requiring every word in one of its usual forms:
// the stem as a prefix, bare or after an article or a conjunction. The
// texts write "صورية", "والصورية", "الصوري"; an MCP client searching
// "الصورية سند الضد" found nothing (23 Oct 2026) because every word had to
// be that exact token. Little words are left out; numbers stay exact.
export function fts5Forms(q: string): string {
  const groups: string[] = [];
  for (const raw of arNorm(q).trim().split(/\s+/)) {
    const w = raw.replace(/^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$/gu, "");
    if (w === "" || AR_STOP.has(w)) continue;
    const stem = lightStem(w);
    if (!/[؀-ۿ]/.test(w) || [...stem].length < 3) {
      groups.push(fts5Quote(w));
      continue;
    }
    // the word as typed stays a form: "وكاله" is not "و" + "كاله"
    const forms = new Set(FORM_PREFIXES.map((p) => `${fts5Quote(p + stem)}*`));
    forms.add(fts5Quote(w));
    groups.push(`(${[...forms].join(" OR ")})`);
  }
  return groups.join(" AND ");
}
