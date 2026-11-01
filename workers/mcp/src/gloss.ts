// The cheap parts of the website's zero-result rescue (internal/site/
// searchfallback.go, gloss.go, translit.go), ported for the Worker, which
// has no Postgres: the glossary of English and French legal terms, Arabic-
// Indic digit folding, a number and a year pulled out of the question, and
// spelling variants of Latin names. It reads the question into other words
// to search for; it never writes anything shown as law.
//
// gloss.json is generated from the Go maps (go generate ./internal/site, and
// a Go test fails when it is stale), so the two cannot drift.

import gloss from "./gloss.json";
import { arNorm } from "./arnorm";

const PHRASES: Record<string, string> = gloss.phrases;
const WORDS: Record<string, string> = gloss.words;
const PHRASE_KEYS: string[] = gloss.phrase_keys; // longest first
const STOP = new Set<string>(gloss.stop);
const PINS: Record<string, number> = gloss.pins;

const AR_DIGITS: Record<string, string> = {};
"٠١٢٣٤٥٦٧٨٩".split("").forEach((c, i) => (AR_DIGITS[c] = String(i)));
"۰۱۲۳۴۵۶۷۸۹".split("").forEach((c, i) => (AR_DIGITS[c] = String(i)));

// foldDigits turns Arabic-Indic and Persian digits into 0-9: the indexed texts use 0-9.
export function foldDigits(s: string): string {
  return s.replace(/[٠-٩۰-۹]/g, (c) => AR_DIGITS[c]);
}

export const hasArabic = (s: string) => /[؀-ۿ]/.test(s);

// latinFold lower-cases a Latin query, straightens curly apostrophes and
// strips accents ("nationalité" is "nationalite").
export function latinFold(s: string): string {
  return s.replace(/[’‘]/g, "'").normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();
}

export function latinTokens(q: string): string[] {
  return latinFold(q).split(/[^\p{L}\p{N}'-]+/u).filter(Boolean).map((t) => t.replace(/'s$/, ""));
}

// glossQueryWhole returns the Arabic search for a Latin query ("" when it is
// Arabic or has no word the glossary knows) and whether every content word was
// translated. Unknown words are left out when others are known.
export function glossQueryWhole(q: string): { ar: string; whole: boolean } {
  if (q.trim() === "" || hasArabic(q)) return { ar: "", whole: false };
  let padded = ` ${latinTokens(q).join(" ")} `;
  const out: string[] = [];
  for (const p of PHRASE_KEYS) {
    if (padded.includes(` ${p} `)) {
      out.push(PHRASES[p]);
      padded = padded.split(` ${p} `).join(" ");
    }
  }
  let whole = true;
  for (const w of padded.split(" ").filter(Boolean)) {
    if (STOP.has(w)) continue;
    if (Object.hasOwn(WORDS, w)) out.push(WORDS[w]);
    else whole = false;
  }
  return { ar: out.join(" "), whole: whole && out.length > 0 };
}

export const glossQuery = (q: string) => glossQueryWhole(q).ar;

const SEARCH_YEAR = /^(?:19|20)\d\d$/;
const SEARCH_NUM = /^\d{1,5}$/;
// little Arabic words that no title search should be held up by
const AR_STOP = new Set(["في", "من", "على", "عن", "الى", "إلى", "او", "أو", "و", "ان", "أن", "هل", "ما", "رقم", "تاريخ"]);

// splitNumbers pulls a text number and a year out of a query, leaving the words.
export function splitNumbers(q: string): { num: string; year: number; words: string[] } {
  let num = "";
  let year = 0;
  const words: string[] = [];
  for (const w of foldDigits(q).split(/\s+/).filter(Boolean)) {
    if (SEARCH_YEAR.test(w) && year === 0) year = Number(w);
    else if (SEARCH_NUM.test(w) && num === "") num = w;
    else if (AR_STOP.has(w)) continue;
    else words.push(w);
  }
  return { num, year, words };
}

// nameVariants gives the other ways a Latin name is spelled in English titles
// ("Chehab", "Shehab", "Shihab"; "Khalil", "Halil"), the original first. The
// Worker has no list of title words to check a guess against, so each variant
// is tried against the English titles and kept only if it finds something.
export function nameVariants(word: string): string[] {
  const w = latinFold(word).replace(/[^a-z]/g, "");
  if (w.length < 3) return [];
  const out = new Set<string>([w]);
  const swaps: [RegExp, string[]][] = [
    [/^ch/, ["sh"]], [/^sh/, ["ch"]], [/kh/, ["h", "ch"]], [/ou/, ["u", "o"]], [/aa/, ["a"]], [/ee/, ["i"]],
    [/ie/, ["i", "e"]], [/ei/, ["i", "e"]], [/e/, ["i", "a"]], [/dj|j/, ["g"]], [/^k/, ["c", "q"]], [/ae/, ["a", "e"]],
  ];
  for (const [re, subs] of swaps) {
    if (!re.test(w)) continue;
    for (const sub of subs) out.add(w.replace(re, sub));
  }
  // one spelling change on top of a digraph change: Chehab to Shihab
  for (const v of [...out]) {
    if (v !== w && /e/.test(v)) out.add(v.replace(/e/, "i"));
  }
  return [...out];
}

const PIN_AR_STOP = new Set(["قانون", "القانون", "لبنان", "في"]);

// pinKey folds a question to the form the pinned terms are kept in (the twin
// of pinKey in internal/site/glosspins.go): Latin lower case, accents and
// hyphens dropped, the little words left out; Arabic with spelling folded,
// the article ال of each word and the words قانون and لبنان left out.
export function pinKey(q: string): string {
  if (hasArabic(q)) {
    return arNorm(q).split(/\s+/).filter((w) => w && !PIN_AR_STOP.has(w))
      .map((w) => {
        while ([...w].length > 4 && w.startsWith("ال")) w = [...w].slice(2).join(""); // "الالكترونية" and "الكترونية" are one word
        return w;
      }).join(" ");
  }
  return latinTokens(q.replace(/-/g, " ")).filter((t) => !STOP_PIN.has(t)).join(" ");
}

// the little words glossStop holds (not the Arabizi ones the rescue also sets aside)
const STOP_PIN = new Set<string>(gloss.pin_stop);

// pinnedLaw returns the id of the text a question names, or 0 (see pins in gloss.json).
export function pinnedLaw(q: string): number {
  const k = pinKey(q);
  return k !== "" && Object.hasOwn(PINS, k) ? PINS[k] : 0;
}
