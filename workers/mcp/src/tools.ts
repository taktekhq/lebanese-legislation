// Tool implementations, one per MCP tool (see toolDefs in src/mcp.ts for
// the schemas tools/list advertises). Every query reads the D1 snapshot
// only (env.DB); nothing here writes, and no text is paraphrased: every
// text_ar / text_fr field is copied straight out of a row.
//
// Ported from the Go draft (internal/site/mcp.go, api.go, links.go), not
// wired into the Go server: the tool names, descriptions and shapes come
// from there, so an MCP client sees the same thing that file would have
// served, from D1 instead of Postgres.

import { AR_STOP, arNorm, fts5Forms, fts5MatchAll, fts5Quote, lightStem } from "./arnorm";
import { foldDigits, hasArabic, glossQueryWhole, pinnedLaw, latinTokens, nameVariants, splitNumbers } from "./gloss";
import { numberForms } from "./articlenum";
export { numberForms };
import { BASE, LawSummary, LegislationRow, articleURL, clampInt, lawSummary, toolError, withAdvice } from "./db";

export type ToolResult = { body: unknown; isError: boolean };

function ok(body: unknown): ToolResult {
  return { body: withAdvice(body), isError: false };
}
function err(message: string): ToolResult {
  return { body: toolError(message), isError: true };
}

const CHANGE_RELATIONS = ["replace", "amend", "repeal", "insert", "suspend", "extend"];

const VERB: Record<string, string> = {
  replace: "Replaced or reworded",
  amend: "Edited in part",
  repeal: "Repealed",
  insert: "Added to",
  suspend: "Suspended",
  extend: "Extended",
};

async function loadLaw(db: D1Database, id: number): Promise<LegislationRow | null> {
  return db.prepare("SELECT * FROM legislation WHERE id = ?").bind(id).first<LegislationRow>();
}

// ---------------------------------------------------------------- search_legislation

interface SearchArgs {
  query?: string;
  kind?: "laws" | "articles" | "rulings" | "gazette";
  year?: number;
  limit?: number;
}

interface Found {
  laws?: unknown[];
  law_count?: number;
  articles?: unknown[];
  article_count?: number;
  rulings?: unknown[];
  ruling_count?: number;
  gazette?: unknown[];
  gazette_count?: number;
  // the code the question named when none of its articles holds the other
  // words: the articles listed are from other texts
  articles_not_in_code?: string;
}

// One pass of the four searches. num narrows laws by their own number; with
// lawsOnly (a number or a year in a rescued question names a text, not an
// article or a ruling) only the laws are searched. forms matches each word in
// its usual forms (fts5Forms) instead of as typed: a late fallback, as short
// stems ("بيع", "عقد") match more than the question meant.
async function searchOnce(db: D1Database, q: string, kind: string | undefined, year: number | undefined, limit: number, num = "", lawsOnly = false, forms = false): Promise<Found> {
  const out: Found = {};
  const want = (k: string) => kind === undefined || kind === k;
  if (want("laws")) {
    const r = await findLaws(db, q, year, limit, num, forms);
    out.laws = r.rows;
    out.law_count = r.count;
  }
  if (want("articles") && !lawsOnly) {
    const r = await findArticles(db, q, limit, forms);
    out.articles = r.rows;
    out.article_count = r.count;
    if (r.notInCode && r.count > 0) out.articles_not_in_code = r.notInCode;
  }
  if (want("rulings") && !lawsOnly) {
    const r = await findRulings(db, q, limit, forms);
    out.rulings = r.rows;
    out.ruling_count = r.count;
  }
  if (want("gazette") && !lawsOnly) {
    const r = await findGazette(db, q, limit, year);
    out.gazette = r.rows;
    out.gazette_count = r.count;
  }
  return out;
}

const total = (f: Found) => (f.law_count ?? 0) + (f.article_count ?? 0) + (f.ruling_count ?? 0) + (f.gazette_count ?? 0);

export async function searchLegislation(db: D1Database, args: SearchArgs): Promise<ToolResult> {
  // Arabic-Indic digits ("٢٤٨") are folded to 0-9: the indexed texts use 0-9.
  const q = foldDigits((args.query ?? "").trim());
  if (q === "" || [...q].length >= 200) {
    return ok({ q, laws: [], law_count: 0, articles: [], article_count: 0, rulings: [], ruling_count: 0, gazette: [], gazette_count: 0 });
  }
  const limit = clampInt(args.limit, 10, 1, 20);
  const kind = args.kind;
  if (kind !== undefined && !["laws", "articles", "rulings", "gazette"].includes(kind)) {
    return err('kind must be one of "laws", "articles", "rulings", "gazette", or omitted');
  }

  const out: Record<string, unknown> = { q };
  // The fallbacks below stop starting new searches past this point, so the
  // call stays inside its 5 s budget (CALL_TIMEOUT_MS in index.ts).
  const deadline = Date.now() + 2500;
  let found = await searchOnce(db, q, kind, args.year, limit);
  if (total(found) === 0 && kind !== "gazette") {
    // Nothing matched: read the question again, as the website does (see
    // gloss.ts). A year given as a filter stays one: "قانون العفو" in 2026
    // found nothing before, as no rescue ran with a year (23 Oct 2026).
    for (const alt of rescueReadings(q)) {
      const named = alt.num !== "" || alt.year !== 0;
      const r = await searchOnce(db, alt.query, kind, alt.year || args.year, limit, alt.num, named && (kind === undefined || kind === "laws"));
      if (total(r) > 0) {
        found = r;
        out.searched_instead = [alt.query, alt.num, alt.year ? String(alt.year) : ""].filter(Boolean).join(" ");
        out.searched_instead_kind = alt.kind;
        break;
      }
    }
  }
  if (total(found) === 0 && kind !== "gazette" && hasArabic(q)) {
    // Every word in its usual forms: "تبلغه الموعدين تغيبه", "مرور الزمن الصورية".
    const r = await searchOnce(db, q, kind, args.year, limit, "", false, true);
    if (total(r) > 0) {
      found = r;
      out.searched_instead = `${q} (each word in any of its forms)`;
      out.searched_instead_kind = "adjusted";
    }
  }
  if (total(found) === 0 && kind !== "gazette" && hasArabic(q) && Date.now() < deadline) {
    // Still nothing: no text holds every word. Leave out one word, trying
    // the one that tells least first (dropOrder), and keep the first reading
    // that finds anything; then the same with the commonest word gone, while
    // two words are left. "الصورية سند الضد" holds more than any one article
    // says, and only leaving out «الضد» finds the articles on simulation.
    let words = q.split(/\s+/).filter((w) => w !== "" && !AR_STOP.has(arNorm(w)));
    rounds: for (let round = 0; round < 2 && words.length > 1; round++) {
      const order = await dropOrder(db, words, kind);
      for (const i of order) {
        if (Date.now() > deadline) break rounds;
        const left = words.filter((_, j) => j !== i);
        // one kind word left alone ("قانون") answers nothing
        if (left.length === 1 && KIND_WORDS.has(arNorm(left[0]))) continue;
        const rest = left.join(" ");
        const r = await searchOnce(db, rest, kind, args.year, limit, "", false, true);
        if (total(r) > 0) {
          found = r;
          out.searched_instead = rest;
          out.searched_instead_kind = "adjusted";
          break rounds;
        }
      }
      words = words.filter((_, j) => j !== order[0]);
    }
  }
  Object.assign(out, found);
  return ok(out);
}

const KIND_WORDS = new Set(["قانون", "القانون", "مرسوم", "المرسوم", "قرار", "القرار", "نظام", "النظام", "قوانين", "القوانين"]);

// dropOrder ranks the words by how little they tell, as the website's
// dropOne does: a word found in no text of the kind searched (articles when
// no kind is given) first, then the one found in the most, counted in its
// usual forms.
async function dropOrder(db: D1Database, words: string[], kind: string | undefined): Promise<number[]> {
  const [table, col] = kind === "laws" ? ["law_search", "title_norm"] : kind === "rulings" ? ["ruling_search", "text_norm"] : ["article_search", "text_norm"];
  const counts: number[] = [];
  for (const w of words) {
    const m = fts5Forms(w);
    const row = m ? await db.prepare(`SELECT count(*) AS n FROM (SELECT 1 FROM ${table} WHERE ${col} MATCH ? LIMIT 20000)`).bind(m).first<{ n: number }>() : null;
    counts.push(row?.n ? row.n : Infinity);
  }
  return words.map((_, i) => i).sort((a, b) => counts[b] - counts[a] || a - b);
}

// latin folds a Latin-script query for matching code names: lower case,
// accents dropped ("Code pénal" matches "code penal").
function latin(s: string): string {
  return s.normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase().replace(/\s+/g, " ").trim();
}
const hasLatin = (s: string) => /[A-Za-zÀ-ÿ]/.test(s);

// rankCodes keeps the codes whose names hold every word of a Latin-script
// query, closest first: a name equal to the query, then one name holding
// every word (the shorter, the closer), then words spread over the English
// name, the French name and the slug together.
export function rankCodes<T extends { code_name_en?: string | null; code_name_fr?: string | null; code_slug?: string | null }>(q: string, codes: T[]): T[] {
  const query = latin(q);
  const words = query.split(" ").map((w) => w.replace(/s$/, ""));
  const holds = (name: string) => words.every((w) => name.includes(w));
  const ranked: { code: T; score: number }[] = [];
  for (const c of codes) {
    const names = [c.code_name_en, c.code_name_fr, c.code_slug?.replace(/-/g, " ")].filter((n): n is string => !!n).map(latin);
    let score = Infinity;
    for (const n of names) {
      if (n === query) score = 0;
      else if (holds(n)) score = Math.min(score, n.split(" ").length);
    }
    if (score === Infinity && holds(names.join(" "))) score = 1000;
    if (score < Infinity) ranked.push({ code: c, score });
  }
  return ranked.sort((a, b) => a.score - b.score).map((r) => r.code);
}

async function findLaws(db: D1Database, q: string, year: number | undefined, limit: number, num = "", forms = false) {
  const found: LegislationRow[] = [];
  // A question that names one text ("cybercrime" is Law 81 of 2018) has it first (pins in gloss.json).
  const pin = num === "" ? pinnedLaw(q) : 0;
  if (pin) {
    const row = await loadLaw(db, pin);
    if (row) found.push(row);
  }
  // A law's id, as get_law and the site's addresses use it.
  if (num === "" && /^\d{1,10}$/.test(q)) {
    const byID = await loadLaw(db, Number(q));
    if (byID) found.push(byID);
  }
  // The codes by their English or French name ("Penal Code", "obligations"),
  // closest name first: "Penal Code" is the Penal Code before the Military
  // Penal Code and the Code de procédure pénale.
  if (num === "" && hasLatin(q)) {
    const codes = await db.prepare("SELECT * FROM legislation WHERE code_slug IS NOT NULL").all<LegislationRow & { code_name_fr?: string | null }>();
    found.push(...rankCodes(q, codes.results));
  }
  // The year given as a filter is not a title word too: "عفو عام 2026" with
  // year 2026 found nothing, as no title says «2026» (23 Oct 2026).
  const less = year && year > 0 ? q.split(/\s+/).filter((w) => w !== String(year)).join(" ") : q;
  const titleQ = less.trim() === "" ? q : less;
  {
    const match = forms ? fts5Forms(titleQ) : fts5MatchAll(titleQ);
    let sql = match
      ? `SELECT l.* FROM law_search s JOIN legislation l ON l.id = s.law_id WHERE s.title_norm MATCH ?`
      : `SELECT l.* FROM legislation l WHERE 1 = 1`;
    const bind: unknown[] = match ? [match] : [];
    if (year && year > 0) {
      sql += ` AND l.issued_on LIKE ?`;
      bind.push(`${year}-%`);
    }
    if (num !== "") {
      sql += ` AND l.number = ?`;
      bind.push(num);
    }
    sql += ` ORDER BY (l.code_slug IS NOT NULL) DESC, l.issued_on DESC LIMIT ?`;
    bind.push(limit);
    if (match || num !== "") found.push(...(await db.prepare(sql).bind(...bind).all<LegislationRow>()).results);
  }
  // The unofficial English titles, after every Arabic match (Arabic is official).
  const en = hasLatin(q) && num === "" ? fts5English(q) : "";
  if (en) {
    let sql = `SELECT l.* FROM law_search_en s JOIN legislation l ON l.id = s.law_id WHERE s.title_en MATCH ?`;
    const bind: unknown[] = [en];
    if (year && year > 0) {
      sql += ` AND l.issued_on LIKE ?`;
      bind.push(`${year}-%`);
    }
    sql += ` ORDER BY (l.code_slug IS NOT NULL) DESC, s.rank LIMIT ?`;
    bind.push(limit);
    found.push(...await tolerant(async () => (await db.prepare(sql).bind(...bind).all<LegislationRow>()).results));
  }
  const seen = new Set<number>();
  const rows = found.filter((l) => !seen.has(l.id) && seen.add(l.id))
    .filter((l) => !(year && year > 0) || (l.issued_on ?? "").startsWith(`${year}-`))
    .slice(0, limit);
  return { rows: rows.map(lawSummary), count: rows.length };
}

interface ArticleHit {
  law: LawSummary;
  article_number: string;
  snippet: string;
  calculated_current?: CalculatedCurrent;
  url: string;
}

// fts5French builds a MATCH for the French texts: every word, as a prefix
// once it is long enough, with a plural "s" dropped ("obligations" finds
// "obligation" and "obligations").
export function fts5French(q: string): string {
  const words = q.normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase()
    .split(/[^a-z0-9]+/).filter((w) => w.length > 1);
  return words.map((w) => {
    if (w.length >= 5 && w.endsWith("s")) w = w.slice(0, -1);
    return w.length >= 4 ? `${fts5Quote(w)}*` : fts5Quote(w);
  }).join(" AND ");
}

// fts5English builds a MATCH for the English titles: every content word, as a
// prefix once it is long enough, with a plural "s" dropped.
export function fts5English(q: string): string {
  const words = latinTokens(q).flatMap((w) => w.split(/[^a-z0-9]+/)).filter((w) => w.length > 1 && !ENGLISH_STOP.has(w));
  return words.map((w) => {
    if (w.length >= 5 && w.endsWith("s")) w = w.slice(0, -1);
    return w.length >= 3 ? `${fts5Quote(w)}*` : fts5Quote(w);
  }).join(" AND ");
}
const ENGLISH_STOP = new Set(["the", "of", "a", "an", "in", "and", "on", "for", "to", "by", "with", "is", "are"]);

interface Reading {
  query: string;
  num: string;
  year: number;
  kind: "translated" | "adjusted";
}

// rescueReadings lists, cheapest first, the other ways to read a question
// that found nothing (the Worker's share of internal/site/searchfallback.go):
// Latin terms to Arabic through the glossary, a number or a year in the
// question used as a filter on the texts, Latin names in other spellings
// against the English titles, a common leading word set aside. The caller
// uses the first that finds anything and says so.
export function rescueReadings(q: string): Reading[] {
  const { num, year, words } = splitNumbers(q);
  const out: Reading[] = [];
  const seen = new Set<string>([`${q}||`]);
  const add = (query: string, n: string, y: number, kind: Reading["kind"]) => {
    query = query.trim();
    const key = `${query}|${n}|${y}`;
    if ((query === "" && n === "" && y === 0) || seen.has(key)) return;
    seen.add(key);
    out.push({ query, num: n, year: y, kind });
  };
  const latinQ = !hasArabic(q);
  let ar = words;
  let kind: Reading["kind"] = "adjusted";
  if (latinQ) {
    const g = glossQueryWhole(words.join(" "));
    if (g.ar !== "") {
      ar = g.ar.split(/\s+/);
      if (num === "" && year === 0) kind = "translated";
    } else {
      ar = [];
    }
  }
  if (ar.length > 0 || num !== "" || year !== 0) add(ar.join(" "), num, year, kind);
  if (ar.length > 0 && (num !== "" || year !== 0)) add(ar.join(" "), "", 0, "adjusted");
  // a year with fewer words, or the first word (often the kind of text, "قانون") set aside
  if (ar.length > 1) {
    if (year !== 0 && num === "") add(ar.slice(0, -1).join(" "), "", year, "adjusted");
    add(ar.slice(1).join(" "), "", 0, "adjusted");
  }
  // names: other spellings of each Latin word, when the glossary knew none of them
  if (latinQ && ar.length === 0) {
    for (const w of words.slice(0, 3)) {
      for (const v of nameVariants(w).slice(1)) add(words.map((x) => (x === w ? v : x)).join(" "), "", 0, "adjusted");
    }
  }
  return out.slice(0, 6);
}

type ArticleHitRow = LegislationRow & { article_law_id: number; article_number: string; article_text: string };

// a.number is aliased (article_number) because l.* also carries the LAW's own
// printed "number" column; D1 rows key by column name, so an unaliased
// a.number would be silently overwritten by l.number.
const ARTICLE_COLS = `a.law_id AS article_law_id, a.number AS article_number, a.text_ar AS article_text, l.*`;

// articleMatch runs one MATCH on the Arabic articles, codes first, then the
// best match (bm25); lawID keeps it to one text.
async function articleMatch(db: D1Database, match: string, limit: number, lawID = 0): Promise<ArticleHitRow[]> {
  const sql = `SELECT ${ARTICLE_COLS} FROM article_search s
    JOIN legislation_article a ON a.law_id = s.law_id AND a.number = s.number
    JOIN legislation l ON l.id = a.law_id
    WHERE s.text_norm MATCH ?${lawID ? " AND s.law_id = ?" : ""}
    ORDER BY (l.code_slug IS NOT NULL) DESC, s.rank LIMIT ?`;
  const bind: unknown[] = lawID ? [match, lawID, limit] : [match, limit];
  return (await db.prepare(sql).bind(...bind).all<ArticleHitRow>()).results;
}

interface CodeRef {
  id: number;
  title: string;
  numbers: string[]; // article numbers the question names ("المادة 257", "670 671")
  rest: string; // the question less the code's name, the article words and the numbers
}

let codeCache: { id: number; title: string; code_slug: string }[] | null = null;

// Short names the codes go by besides their titles.
const CODE_ALIASES: Record<string, string[]> = {
  "civil-procedure": ["اصول مدنيه"], // "257 أصول مدنية"
  "criminal-procedure": ["اصول جزائيه"],
};

// CODE_WORDS are words of a code's name that say nothing on their own.
// As written (after arNorm), not stemmed: lightStem("قانون") is "قان".
const CODE_WORDS = new Set(["قانون", "القانون", "نظام", "النظام", "اللبناني", "الجديد", "جديد"]); // "قانون السير" is «قانون السير الجديد»
const ARTICLE_WORDS = new Set(["الماده", "ماده", "المواد", "مواد", "م"]);

// codeIn finds a code named in an Arabic question, however it is written:
// "أصول محاكمات مدنية" and "قانون اصول المحاكمات المدنية" are both the Code
// of Civil Procedure, "موجبات وعقود" the Code of Obligations. Every word of
// the name must be there; the longest name wins ("قانون العقوبات العسكري"
// over "قانون العقوبات").
export async function codeIn(db: D1Database, q: string): Promise<CodeRef | null> {
  if (!hasArabic(q)) return null;
  if (!codeCache) {
    codeCache = (await db.prepare("SELECT id, title, code_slug FROM legislation WHERE code_slug IS NOT NULL").all<{ id: number; title: string; code_slug: string }>()).results;
  }
  const words = arNorm(q).split(/\s+/).map((w) => w.replace(/^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$/gu, "")).filter(Boolean);
  const stems = words.map(lightStem);
  let best: { id: number; title: string; name: string[] } | null = null;
  for (const c of codeCache) {
    for (const n of [c.title, ...(CODE_ALIASES[c.code_slug] ?? [])]) {
      const name = arNorm(n).split(/\s+/).filter((w) => w !== "" && !CODE_WORDS.has(w) && !AR_STOP.has(w)).map(lightStem);
      if (name.length > 0 && name.every((s) => stems.includes(s)) && (!best || name.length > best.name.length)) {
        best = { id: c.id, title: c.title, name };
      }
    }
  }
  if (!best) return null;
  const name = new Set(best.name);
  const numbers: string[] = [];
  const rest: string[] = [];
  words.forEach((w, i) => {
    if (/^\d{1,4}(?:[-/]\d{1,3})?$/.test(w) && !/^(?:18|19|20)\d\d$/.test(w)) numbers.push(w.replace("/", "-"));
    else if (!name.has(stems[i]) && !CODE_WORDS.has(w) && !ARTICLE_WORDS.has(w) && !AR_STOP.has(w)) rest.push(w);
  });
  return { id: best.id, title: best.title, numbers, rest: rest.join(" ") };
}

async function findArticles(db: D1Database, q: string, limit: number, forms = false) {
  const hits: ArticleHitRow[] = [];
  const add = (rows: ArticleHitRow[]) => {
    for (const r of rows) {
      if (hits.length < limit && !hits.some((h) => h.article_law_id === r.article_law_id && h.article_number === r.article_number)) hits.push(r);
    }
  };
  // A code named in the question: the articles it numbers, then its own
  // articles holding the other words. "قانون العقوبات 670 671" found no
  // article (no article says «قانون العقوبات»), nor did "المادة 257 أصول
  // محاكمات مدنية صورية" (23 Oct 2026).
  let words = q;
  const code = await codeIn(db, q);
  if (code) {
    if (code.numbers.length > 0) {
      const marks = code.numbers.map(() => "?").join(", ");
      const rows = await db.prepare(`SELECT ${ARTICLE_COLS} FROM legislation_article a JOIN legislation l ON l.id = a.law_id
        WHERE a.law_id = ? AND a.number IN (${marks})`).bind(code.id, ...code.numbers).all<ArticleHitRow>();
      add(code.numbers.flatMap((n) => rows.results.filter((r) => r.article_number === n)));
    }
    if (code.rest !== "") {
      for (const m of [fts5MatchAll(code.rest), fts5Forms(code.rest)]) {
        if (!m || hits.length >= limit) continue;
        const before = hits.length;
        add(await articleMatch(db, m, limit, code.id));
        if (hits.length > before) break;
      }
      words = code.rest;
    }
    if (hits.length > 0) return articleOut(db, hits);
  }
  const notInCode = code?.title;
  const m = forms ? fts5Forms(words) : fts5MatchAll(words);
  if (m) add(await articleMatch(db, m, limit));
  const fr = hasLatin(q) ? fts5French(q) : "";
  if (fr && hits.length < limit) {
    const sql = `SELECT a.law_id AS article_law_id, a.number AS article_number, a.text_fr AS article_text, l.* FROM article_search_fr s
      JOIN legislation_article a ON a.law_id = s.law_id AND a.number = s.number
      JOIN legislation l ON l.id = a.law_id
      WHERE s.text_fr MATCH ?
      ORDER BY (l.code_slug IS NOT NULL) DESC, s.rank LIMIT ?`;
    add(await tolerant(async () => (await db.prepare(sql).bind(fr, limit - hits.length).all<ArticleHitRow>()).results));
  }
  return articleOut(db, hits, notInCode);
}

async function articleOut(db: D1Database, hits: ArticleHitRow[], notInCode?: string) {
  const out: ArticleHit[] = await Promise.all(hits.map(async (r) => ({
    law: lawSummary(r),
    article_number: r.article_number,
    snippet: snippet(r.article_text ?? ""),
    // SELECT * supports snapshots predating the optional calculation column.
    calculated_current: r.article_law_id === 244565
      ? await calculatedCurrent(await db.prepare("SELECT * FROM legislation_article WHERE law_id = ? AND number = ? ORDER BY position LIMIT 1")
        .bind(r.article_law_id, r.article_number).first<ArticleRow>(), r.status)
      : undefined,
    url: articleURL(r.article_law_id, r.article_number, r.kind),
  })));
  return { rows: out, count: out.length, notInCode };
}

interface RulingHit {
  id: number;
  title: string;
  court?: string;
  decided_on?: string;
  snippet?: string;
  url: string;
  pdf_url?: string; // the full judgment's scan on the Lebanese University's site
}

async function findRulings(db: D1Database, q: string, limit: number, forms = false) {
  type Row = { id: number; title: string; court: string | null; decided_on: string | null; text_ar: string | null; pdf_url?: string | null };
  // r.* rather than a column list: pdf_url exists only in snapshots built since 21 Oct 2026.
  const sql = `SELECT r.* FROM ruling_search s
    JOIN ruling r ON r.id = s.ruling_id
    WHERE s.text_norm MATCH ? ORDER BY r.decided_on DESC LIMIT ?`;
  const m = forms ? fts5Forms(q) : fts5MatchAll(q);
  const found: Row[] = m ? (await db.prepare(sql).bind(m, limit).all<Row>()).results : [];
  const out: RulingHit[] = found.map((r) => {
    const h: RulingHit = { id: r.id, title: r.title, url: `${BASE}/ruling/${r.id}/` };
    if (r.court) h.court = r.court;
    if (r.decided_on) h.decided_on = r.decided_on;
    if (r.text_ar) h.snippet = snippet(r.text_ar);
    if (r.pdf_url) h.pdf_url = r.pdf_url;
    return h;
  });
  return { rows: out, count: out.length };
}

// gazetteNumber is the issue number a question asks for: the question is a
// number, or names the Gazette and a number ("الجريدة الرسمية عدد 39").
// Anything else names no issue. The whole question used to go into the LIKE
// pattern, and D1 refuses one over 50 bytes: a long Arabic search failed
// with "LIKE or GLOB pattern too complex" (3 calls, 23 Oct 2026).
export function gazetteNumber(q: string): string {
  const t = foldDigits(q).trim();
  if (/^\d{1,6}$/.test(t)) return t;
  if (!/جريد|gazette|journal|عدد/i.test(t)) return "";
  return t.match(/\d{1,6}/)?.[0] ?? "";
}

// gazetteQuery reads what a Gazette search asks for: a bare year ("2025",
// 1900 to 2100) is the issues of that year, "22/2025" is issue 22 of 2025,
// anything else is an issue number as gazetteNumber reads it.
export function gazetteQuery(q: string): { num: string; year: number } {
  const t = foldDigits(q).trim();
  if (/^\d{4}$/.test(t) && Number(t) >= 1900 && Number(t) <= 2100) return { num: "", year: Number(t) };
  const ny = t.match(/^(\d{1,4})\s*[/\-]\s*((?:19|20)\d\d)$/);
  if (ny) return { num: String(Number(ny[1])), year: Number(ny[2]) };
  return { num: gazetteNumber(q), year: 0 };
}

async function findGazette(db: D1Database, q: string, limit: number, yearArg?: number) {
  const parsed = gazetteQuery(q);
  const year = parsed.year || (yearArg && yearArg >= 1900 && yearArg <= 2100 ? Math.trunc(yearArg) : 0);
  const num = parsed.num;
  if (num === "" && year === 0) return { rows: [], count: 0 };
  const where: string[] = [];
  const bind: unknown[] = [];
  if (num !== "") {
    // a year in the question makes the number exact: issue 22 of 2025
    if (parsed.year) {
      where.push("number = ?");
      bind.push(num);
    } else {
      where.push("number LIKE ?");
      bind.push(`%${num}%`);
    }
  }
  if (year) {
    where.push("published_on >= ? AND published_on <= ?");
    bind.push(`${year}-01-01`, `${year}-12-31`);
  }
  const w = where.join(" AND ");
  const countRow = await db.prepare(`SELECT count(*) AS n FROM gazette_issue WHERE ${w}`).bind(...bind).first<{ n: number }>();
  const rows = await db.prepare(`SELECT id, number, published_on FROM gazette_issue WHERE ${w} ORDER BY published_on DESC, id DESC LIMIT ?`)
    .bind(...bind, limit).all<{ id: number; number: string | null; published_on: string | null }>();
  const out = rows.results.map((r) => ({
    id: r.id,
    number: r.number ?? undefined,
    published_on: r.published_on ?? undefined,
    url: `${BASE}/gazette/${r.id}/`,
  }));
  return { rows: out, count: countRow?.n ?? out.length };
}

function snippet(text: string, maxChars = 220): string {
  const t = text.trim();
  if (t.length <= maxChars) return t;
  return t.slice(0, maxChars).trimEnd() + "…";
}

// ---------------------------------------------------------------- get_law

// Enough for every code (the Penal Code has 787 articles, the Code of
// Obligations 1,100 or so); a longer list says it was cut.
const MAX_LIST_ITEMS = 2000;

// tolerant runs a query that reads a table or column newer than some D1
// snapshots (legislation_section, section_id, article_search_fr): on a
// snapshot without it, the tool answers without that part instead of
// failing.
async function tolerant<T>(q: () => Promise<T[]>): Promise<T[]> {
  try {
    return await q();
  } catch (e) {
    if (/no such (table|column)/i.test(String(e))) return [];
    throw e;
  }
}

// heading tidies a chapter heading the way the site shows it
// (internal/site's Heading): "الباب الاول :- في" becomes "الباب الاول: في".
export function heading(h: string): string {
  h = h.replace(/\s*:\s*[-:]?\s*-?\s*/g, ": ").replace(/\s*-\s*-\s*/g, " - ");
  return h.trim().replace(/:+$/, "").trim();
}

interface Section { id: number; parent_id?: number; depth: number; heading: string; note?: string }

// lawSections: a text's chapters; note is the Centre's editorial note on a
// chapter ("اضيف هذا الجزء … بموجب …"). SELECT * so a snapshot without the
// note column still answers.
async function lawSections(db: D1Database, id: number): Promise<Section[]> {
  const rows = await tolerant(async () => (await db.prepare(
    "SELECT * FROM legislation_section WHERE law_id = ? ORDER BY position",
  ).bind(id).all<{ id: number; parent_id: number | null; depth: number; heading: string; note?: string | null }>()).results);
  return rows.map((r) => {
    const s: Section = { id: r.id, depth: r.depth, heading: heading(r.heading) };
    if (r.parent_id != null) s.parent_id = r.parent_id;
    if (r.note) s.note = r.note;
    return s;
  });
}

export async function getLaw(db: D1Database, args: { id?: number }): Promise<ToolResult> {
  const id = args.id;
  if (!id || id <= 0) return err("id must be a positive integer");
  const l = await loadLaw(db, id);
  if (!l) return err(`law ${id} not found`);

  // SELECT * so a snapshot without section_id still answers.
  const articlesRes = await db.prepare(
    "SELECT * FROM legislation_article WHERE law_id = ? ORDER BY position LIMIT ?"
  ).bind(id, MAX_LIST_ITEMS + 1).all<{ position: number; number: string; section_id?: number | null }>();
  const rows = articlesRes.results;
  const truncated = rows.length > MAX_LIST_ITEMS;
  const articles = rows.slice(0, MAX_LIST_ITEMS).map((a) => {
    const out: { position: number; number: string; section_id?: number; url: string } = {
      position: a.position, number: a.number, url: articleURL(id, a.number, l.kind),
    };
    if (a.section_id != null) out.section_id = a.section_id;
    return out;
  });

  const amendedBy = await lawLevelLinks(db, id, "to");
  const amended = await lawLevelLinks(db, id, "from");

  return ok({
    ...lawSummary(l),
    gazette_number: l.gazette_number ?? undefined,
    gazette_date: l.gazette_date ?? undefined,
    gazette_issue_id: l.gazette_issue_id ?? undefined,
    table_of_contents: await lawSections(db, id),
    articles,
    articles_truncated: truncated ? `the first ${MAX_LIST_ITEMS} of ${l.article_count}; get_law_texts pages through all of them` : undefined,
    amended_by: amendedBy,
    amended: amended,
  });
}

// ---------------------------------------------------------------- get_law_texts

interface ArticleRow {
  law_id: number; position: number; number: string; note: string | null; text_ar: string; text_fr: string | null;
  in_force_from: string | null; section_id?: number | null;
  // From article_in_force; absent on a snapshot older than these columns.
  current_text_ar?: string | null; version?: string; state?: string; pending?: number; valid_from?: string | null;
  calculated_current?: string | null;
}

interface CalculatedCurrent {
  kind: "statutory_monetary_adjustment";
  text_ar: string;
  source_text_ar: string;
  source_sha256: string;
  is_published_wording: false;
  multiplier: 50;
  authority: { law_id: 330819; article: "1"; url: string; effective_on: "2024-09-19" };
}

// Go owns the reviewed calculations and spellings. Only validate and return
// its exported record here, checking the exact current source before use.
async function calculatedCurrent(a: ArticleRow | null, lawStatus: string): Promise<CalculatedCurrent | undefined> {
  if (!a || a.law_id !== 244565 || lawStatus !== "in_force" || a.state !== "in_force" || a.pending || !a.calculated_current) return;
  const today = new Date().toISOString().slice(0, 10);
  if (today < "2024-09-19") return;
  for (const date of [a.valid_from, a.in_force_from]) {
    if (date && (!/^\d{4}-\d{2}-\d{2}$/.test(date) || date > today)) return;
  }
  let c: CalculatedCurrent;
  try { c = JSON.parse(a.calculated_current); } catch { return; }
  if (!c || c.kind !== "statutory_monetary_adjustment" || typeof c.text_ar !== "string" || !c.text_ar.trim()
    || c.source_text_ar !== (a.current_text_ar ?? a.text_ar) || typeof c.source_sha256 !== "string"
    || !/^[a-f0-9]{64}$/.test(c.source_sha256) || c.is_published_wording !== false || c.multiplier !== 50
    || c.authority?.law_id !== 330819 || c.authority.article !== "1"
    || c.authority.url !== "https://lebaneselegislation.com/decree/330819/article/1/"
    || c.authority.effective_on !== "2024-09-19") return;
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(c.source_text_ar));
  const hash = Array.from(new Uint8Array(digest), b => b.toString(16).padStart(2, "0")).join("");
  if (hash !== c.source_sha256) return;
  // Whitelist fields so malformed exports cannot introduce extra claims.
  return { kind: c.kind, text_ar: c.text_ar, source_text_ar: c.source_text_ar, source_sha256: c.source_sha256,
    is_published_wording: false, multiplier: 50,
    authority: { law_id: 330819, article: "1", url: c.authority.url, effective_on: "2024-09-19" } };
}

// articleTexts is the wording part of an article, the same rules for
// get_article and get_law_texts: text_ar is the wording in force today.
// When an amendment replaced the Centre's wording, the Centre's (and its
// French, which follows the old wording) move to centre_text_*, so an
// assistant quoting text_ar never quotes a superseded article.
function articleTexts(a: ArticleRow, lawRepealed: boolean) {
  const repealedArticle = a.state === "repealed";
  const amended = !!a.current_text_ar || repealedArticle;
  return {
    text_ar: repealedArticle ? undefined : (a.current_text_ar ?? a.text_ar),
    text_fr: amended ? undefined : (a.text_fr ?? undefined),
    version: a.version ?? undefined,
    current_since: amended ? (a.valid_from ?? undefined) : undefined,
    pending_change: a.pending ? "a later change to this article has no full wording yet; check the Official Gazette" : undefined,
    centre_text_ar: amended && a.text_ar ? a.text_ar : undefined,
    centre_text_fr: amended ? (a.text_fr ?? undefined) : undefined,
    in_force_from: a.in_force_from ?? undefined,
    repealed: lawRepealed || repealedArticle,
  };
}

// centreErrors: notes on errors in the Centre's own text of a law's
// articles (one article repeating another, a stray letter), by number.
async function centreErrors(db: D1Database, lawID: number, number?: string): Promise<Map<string, { en: string; ar: string }>> {
  const rows = await tolerant(async () => (await (number === undefined
    ? db.prepare("SELECT number, note_en, note_ar FROM centre_error WHERE law_id = ?").bind(lawID)
    : db.prepare("SELECT number, note_en, note_ar FROM centre_error WHERE law_id = ? AND number = ?").bind(lawID, number)
  ).all<{ number: string; note_en: string; note_ar: string }>()).results);
  return new Map(rows.map((r) => [r.number, { en: r.note_en, ar: r.note_ar }]));
}

const TEXTS_DEFAULT = 50;
const TEXTS_MAX = 200;

export async function getLawTexts(db: D1Database, args: { law_id?: number; offset?: number; limit?: number }): Promise<ToolResult> {
  const id = args.law_id;
  if (!id || id <= 0) return err("law_id must be a positive integer");
  const l = await loadLaw(db, id);
  if (!l) return err(`law ${id} not found`);
  const offset = clampInt(args.offset, 0, 0, 1_000_000);
  const limit = clampInt(args.limit, TEXTS_DEFAULT, 1, TEXTS_MAX);
  const total = (await db.prepare("SELECT count(*) AS n FROM legislation_article WHERE law_id = ?")
    .bind(id).first<{ n: number }>())?.n ?? 0;
  const rows = (await db.prepare("SELECT * FROM legislation_article WHERE law_id = ? ORDER BY position LIMIT ? OFFSET ?")
    .bind(id, limit, offset).all<ArticleRow>()).results;
  const errors = await centreErrors(db, id);
  const articles = await Promise.all(rows.map(async (a) => ({
    position: a.position,
    number: a.number,
    section_id: a.section_id ?? undefined,
    note: a.note ?? undefined,
    centre_error: errors.get(a.number),
    ...articleTexts(a, l.status === "repealed"),
    calculated_current: await calculatedCurrent(a, l.status),
    url: articleURL(id, a.number, l.kind),
  })));
  const next = offset + rows.length;
  return ok({
    law: lawSummary(l),
    total,
    offset,
    next_offset: next < total ? next : undefined,
    articles,
  });
}

interface LawLevelLink {
  relation: string;
  verb: string;
  law: LawSummary;
  effective_on?: string;
  // The articles of this law the other text touches (amended_by), or the
  // articles of the other law this one touches (amended). Absent when the
  // change is to the law as a whole.
  articles?: string[];
}

// lawLevelLinks: the texts that changed this one (direction "to") or that
// this one changed (direction "from"), oldest first, one entry per relation
// and other text. Edges to or from a single article count too: the Labour
// Code is changed mostly article by article, so articles lists which.
async function lawLevelLinks(db: D1Database, lawID: number, direction: "to" | "from"): Promise<LawLevelLink[]> {
  const relPlaceholders = CHANGE_RELATIONS.map(() => "?").join(",");
  const sql = direction === "to"
    ? `SELECT ge.relation, ge.to_article AS art, l.* FROM graph_edge ge JOIN legislation l ON l.id = ge.from_id
       WHERE ge.to_law_id = ? AND ge.from_type = 'legislation' AND ge.from_id != ?
         AND ge.status IN ('linked','no_article') AND ge.relation IN (${relPlaceholders})
       ORDER BY coalesce(nullif(l.gazette_date, ''), l.issued_on) ASC, l.id ASC, ge.id ASC`
    : `SELECT ge.relation, ge.to_article AS art, l.* FROM graph_edge ge JOIN legislation l ON l.id = ge.to_law_id
       WHERE ge.from_id = ? AND ge.from_type = 'legislation' AND ge.to_law_id != ?
         AND ge.status IN ('linked','no_article') AND ge.relation IN (${relPlaceholders})
       ORDER BY coalesce(nullif(l.gazette_date, ''), l.issued_on) ASC, l.id ASC, ge.id ASC`;
  const rows = await db.prepare(sql).bind(lawID, lawID, ...CHANGE_RELATIONS)
    .all<LegislationRow & { relation: string; art: string | null }>();
  // The date of the change is the amending text's: the other law's for
  // amended_by, this law's for amended.
  const self = direction === "from" ? await loadLaw(db, lawID) : null;
  const byKey = new Map<string, LawLevelLink>();
  for (const r of rows.results) {
    const key = `${r.relation}|${r.id}`;
    let e = byKey.get(key);
    if (!e) {
      e = {
        relation: r.relation,
        verb: VERB[r.relation] ?? "Changed",
        law: lawSummary(r),
        effective_on: ((self ?? r).gazette_date || (self ?? r).issued_on) ?? undefined,
      };
      byKey.set(key, e);
    }
    if (r.art) {
      e.articles ??= [];
      if (!e.articles.includes(r.art)) e.articles.push(r.art);
    }
  }
  const out = [...byKey.values()];
  for (const e of out) e.articles?.sort((x, y) => x.localeCompare(y, "en", { numeric: true }));
  return out;
}

// ---------------------------------------------------------------- get_article

export async function getArticle(db: D1Database, args: { law_id?: number; number?: string }): Promise<ToolResult> {
  const lawID = args.law_id;
  const asked = (args.number ?? "").trim();
  if (!lawID || lawID <= 0 || asked === "") return err("law_id (a positive integer) and number are required");
  const l = await loadLaw(db, lawID);
  if (!l) return err(`law ${lawID} not found`);
  let a: ArticleRow | null = null;
  for (const n of numberForms(asked)) {
    a = await db.prepare("SELECT * FROM legislation_article WHERE law_id = ? AND number = ? ORDER BY position LIMIT 1")
      .bind(lawID, n).first<ArticleRow>();
    if (a) break;
  }
  if (!a) return err(`article ${JSON.stringify(asked)} not found in law ${lawID}`);
  const number = a.number;

  const changes = await articleChanges(db, lawID, number);
  const affected = await articleAffected(db, lawID, number);

  return ok({
    law: lawSummary(l),
    number,
    section_id: a.section_id ?? undefined,
    note: a.note ?? undefined,
    centre_error: (await centreErrors(db, lawID, number)).get(number),
    ...articleTexts(a, l.status === "repealed"),
    calculated_current: await calculatedCurrent(a, l.status),
    changes,
    possibly_affected_by: affected,
    url: articleURL(lawID, number, l.kind),
    source_url: l.source_url ?? undefined,
  });
}

interface ArticleChange {
  relation: string;
  verb: string;
  by: LawSummary;
  by_article: string;
  effective_on?: string;
  text_ar?: string;
  url: string;
}

// articleChanges: every text that changed this article, oldest first, with
// the amending article's own words (internal/site/links.go's attachLinks,
// the main non-note branch; this D1 snapshot does not carry the Centre's
// in-article notes as a separate reversed edge, so that secondary source
// is left out here).
async function articleChanges(db: D1Database, lawID: number, number: string): Promise<ArticleChange[]> {
  const relPlaceholders = CHANGE_RELATIONS.map(() => "?").join(",");
  const sql = `SELECT ge.relation, ge.from_id AS by_id, ge.from_article AS by_article, l.*
    FROM graph_edge ge JOIN legislation l ON l.id = ge.from_id
    WHERE ge.to_law_id = ? AND ge.to_article = ? AND ge.from_id != ?
      AND ge.status IN ('linked','no_article') AND ge.relation IN (${relPlaceholders})
    ORDER BY coalesce(l.gazette_date, l.issued_on) ASC`;
  const rows = await db.prepare(sql).bind(lawID, number, lawID, ...CHANGE_RELATIONS)
    .all<LegislationRow & { relation: string; by_id: number; by_article: string | null }>();
  const out: ArticleChange[] = [];
  // The parser and the model often find the same change: list it once.
  const seen = new Set<string>();
  for (const r of rows.results) {
    const key = `${r.relation}|${r.by_id}|${r.by_article ?? ""}`;
    if (seen.has(key)) continue;
    seen.add(key);
    let text: string | undefined;
    if (r.by_article) {
      const art = await db.prepare("SELECT text_ar FROM legislation_article WHERE law_id = ? AND number = ? ORDER BY position LIMIT 1")
        .bind(r.by_id, r.by_article).first<{ text_ar: string }>();
      text = art?.text_ar;
    }
    out.push({
      relation: r.relation,
      verb: VERB[r.relation] ?? "Changed",
      by: lawSummary(r),
      by_article: r.by_article ?? "",
      effective_on: r.gazette_date ?? r.issued_on ?? undefined,
      text_ar: text,
      url: r.by_article ? articleURL(r.by_id, r.by_article) : `${BASE}/law/${r.by_id}/`,
    });
  }
  return out;
}

interface Affected {
  by: LawSummary;
  by_article: string;
  url: string;
}

async function articleAffected(db: D1Database, lawID: number, number: string): Promise<Affected[]> {
  const rows = await db.prepare(
    `SELECT p.repealing_article, l.* FROM possibly_affected p JOIN legislation l ON l.id = p.repealing_law_id
     WHERE p.law_id = ? AND (p.article_number = ? OR p.article_number IS NULL)
     ORDER BY coalesce(l.gazette_date, l.issued_on)`
  ).bind(lawID, number).all<LegislationRow & { repealing_article: string | null }>();
  return rows.results.map((r) => ({
    by: lawSummary(r),
    by_article: r.repealing_article ?? "",
    url: r.repealing_article ? articleURL(r.id, r.repealing_article) : `${BASE}/law/${r.id}/`,
  }));
}

// ---------------------------------------------------------------- get_ruling

export async function getRuling(db: D1Database, args: { id?: number }): Promise<ToolResult> {
  const id = args.id;
  if (!id || id <= 0) return err("id must be a positive integer");
  const r = await db.prepare("SELECT * FROM ruling WHERE id = ?").bind(id).first<{
    id: number; court: string | null; court_en: string | null; number: string | null; year: number | null;
    decided_on: string | null; title: string; president: string | null; members: string | null;
    text_ar: string | null; source_url: string | null; pdf_url?: string | null;
  }>();
  if (!r) return err(`ruling ${id} not found`);
  const out: Record<string, unknown> = {
    id: r.id, title: r.title, url: `${BASE}/ruling/${r.id}/`,
  };
  if (r.court) out.court = r.court;
  if (r.court_en) out.court_en = r.court_en;
  if (r.number) out.number = r.number;
  if (r.year) out.year = r.year;
  if (r.decided_on) out.decided_on = r.decided_on;
  if (r.president) out.president = r.president;
  if (r.members) {
    try { out.members = JSON.parse(r.members); } catch { /* ignore malformed */ }
  }
  if (r.text_ar) {
    out.text_ar = r.text_ar;
  } else {
    out.note = r.pdf_url
      ? "The Centre holds this judgment as a scanned file only: pdf_url opens it on the Lebanese University's site; " +
        "the ruling page (url) shows a machine reading of it."
      : "The Centre holds this judgment as a file only, not as typed text.";
  }
  // The scan at the source, for every ruling that has one (Nizar, 21 Oct 2026):
  // the original is the reference, so every answer carries it.
  if (r.pdf_url) out.pdf_url = r.pdf_url;
  if (r.source_url) out.source_url = r.source_url;
  // Older snapshots have no ruling_page table, just as some lack pdf_url.
  const pages = await tolerant(async () => (await db.prepare(
    "SELECT n, text_ar, low_confidence FROM ruling_page WHERE ruling_id = ? ORDER BY n"
  ).bind(id).all<{ n: number; text_ar: string; low_confidence: number }>()).results);
  if (pages.length) {
    out.pages = pages.map((p) => ({
      n: p.n,
      text_ar: p.text_ar.replace(/\uE000/g, "⟦").replace(/\uE001/g, " | ").replace(/\uE002/g, "⟧"),
      ...(p.low_confidence ? { low_confidence: true } : {}),
    }));
    out.pages_note = "Machine reading of the scanned judgment. It may contain errors; the scan (pdf_url) is the reference." +
      (pages.some((p) => p.low_confidence)
        ? " Pages with low_confidence: true are hard to read (mostly handwriting): expect errors."
        : "");
  }
  return ok(out);
}

// ---------------------------------------------------------------- get_gazette_issue

export async function getGazetteIssue(db: D1Database, args: { id?: number }): Promise<ToolResult> {
  const id = args.id;
  if (!id || id <= 0) return err("id must be a positive integer");
  const g = await db.prepare("SELECT * FROM gazette_issue WHERE id = ?").bind(id)
    .first<{ id: number; number: string | null; published_on: string | null; source_url: string | null }>();
  if (!g) return err(`gazette issue ${id} not found`);
  const texts = await db.prepare(
    "SELECT l.* FROM gazette_entry e JOIN legislation l ON l.id = e.law_id WHERE e.issue_id = ? ORDER BY l.id"
  ).bind(id).all<LegislationRow>();
  return ok({
    id: g.id,
    number: g.number ?? undefined,
    published_on: g.published_on ?? undefined,
    texts: texts.results.map(lawSummary),
    url: `${BASE}/gazette/${g.id}/`,
    source_url: g.source_url ?? undefined,
  });
}

// ---------------------------------------------------------------- get_links

const KEY_RE = /^(law|ruling|opinion|research|gazette)\/(\d+)(?:\/art\/(.+))?$/;

// A quote is a whole article's wording at times, many KB; 200 of them made
// one oversized answer. Cut to this many characters (quote_truncated says so).
const QUOTE_MAX = 600;

export async function getLinks(
  db: D1Database,
  args: { key?: string; direction?: string; relation?: string; limit?: number; offset?: number; order?: string; scope?: string }
): Promise<ToolResult> {
  const key = (args.key ?? "").trim();
  const m = KEY_RE.exec(key);
  if (!m) {
    return err('key must look like "law/<id>", "law/<id>/art/<number>", "ruling/<id>", "opinion/<id>", "research/<id>" or "gazette/<id>"');
  }
  const direction = args.direction ?? "to";
  if (direction !== "to" && direction !== "from") return err('direction must be "to" or "from"');
  const order = args.order ?? "newest";
  if (order !== "newest" && order !== "oldest") return err('order must be "newest" or "oldest"');
  const isLaw = m[1] === "law" && m[3] === undefined;
  const scope = args.scope ?? (isLaw ? "all" : "law");
  if (scope !== "all" && scope !== "law") return err('scope must be "all" or "law"');
  const limit = clampInt(args.limit, 50, 1, 200);
  const offset = clampInt(args.offset, 0, 0, 1_000_000);

  // A law key with scope "all" takes in the edges of its articles too.
  let cond: string;
  const bind: unknown[] = [key];
  if (isLaw && scope === "all") {
    cond = direction === "to"
      ? "(ge.to_key = ? OR ge.to_law_id = ?)"
      : "(ge.from_key = ? OR (ge.from_type = 'legislation' AND ge.from_id = ?))";
    bind.push(Number(m[2]));
  } else {
    cond = direction === "to" ? "ge.to_key = ?" : "ge.from_key = ?";
  }
  if (args.relation) {
    cond += " AND ge.relation = ?";
    bind.push(args.relation);
  }

  const dir = order === "newest" ? "DESC" : "ASC";
  const dateExpr = "coalesce(nullif(l.gazette_date, ''), l.issued_on)";
  const sql = `SELECT ge.id, ge.from_key, ge.to_key, ge.relation, ge.source, ge.status, ge.to_cite, ge.part, ge.quote,
      ${dateExpr} AS src_date
    FROM graph_edge ge LEFT JOIN legislation l ON ge.from_type = 'legislation' AND l.id = ge.from_id
    WHERE ${cond}
    ORDER BY (${dateExpr} IS NULL), ${dateExpr} ${dir}, ge.id ${dir} LIMIT ? OFFSET ?`;

  type Edge = {
    id: number; from_key: string; to_key: string | null; relation: string; source: string; status: string;
    to_cite: string | null; part: string | null; quote: string | null; src_date: string | null;
  };
  let rows: Edge[];
  let count: number;
  try {
    count = (await db.prepare(`SELECT count(*) AS n FROM graph_edge ge WHERE ${cond}`).bind(...bind).first<{ n: number }>())?.n ?? 0;
    rows = (await db.prepare(sql).bind(...bind, limit, offset).all<Edge>()).results;
  } catch (e) {
    return err(`could not read the links of ${key}: ${String(e).slice(0, 200)}; try a smaller limit`);
  }

  const next = offset + rows.length;
  return ok({
    key,
    direction,
    scope: isLaw ? scope : undefined,
    order,
    total: count,
    offset,
    next_offset: next < count ? next : undefined,
    links: rows.map((e) => {
      const quote = e.quote ? Array.from(e.quote) : [];
      const cut = quote.length > QUOTE_MAX;
      return {
        relation: e.relation,
        source: e.source,
        status: e.status,
        from_key: e.from_key,
        to_key: e.to_key ?? undefined,
        to_cite: e.to_cite ?? undefined,
        part: e.part ?? undefined,
        date: e.src_date ?? undefined,
        quote: e.quote ? (cut ? quote.slice(0, QUOTE_MAX).join("") + "…" : e.quote) : undefined,
        quote_truncated: cut ? true : undefined,
      };
    }),
  });
}

// ---------------------------------------------------------------- list_legislation, list_gazette_issues

// dateBound reads "2025", "2025-03" or "2025-03-17" as the first (or, for an
// upper bound, last) day it covers, or null when it is not a real date.
export function dateBound(v: unknown, upper: boolean): string | null {
  if (typeof v === "number") v = String(v);
  if (typeof v !== "string") return null;
  const m = foldDigits(v.trim()).match(/^(\d{4})(?:-(\d{1,2})(?:-(\d{1,2}))?)?$/);
  if (!m) return null;
  const y = Number(m[1]);
  if (y < 1800 || y > 2200) return null;
  const pad = (n: number) => String(n).padStart(2, "0");
  if (m[2] === undefined) return upper ? `${y}-12-31` : `${y}-01-01`;
  const mo = Number(m[2]);
  if (mo < 1 || mo > 12) return null;
  const last = new Date(Date.UTC(y, mo, 0)).getUTCDate();
  if (m[3] === undefined) return `${y}-${pad(mo)}-${upper ? pad(last) : "01"}`;
  const d = Number(m[3]);
  if (d < 1 || d > last) return null;
  return `${y}-${pad(mo)}-${pad(d)}`;
}

// listRange validates since, until and order the same way for both listings.
function listRange(args: { since?: unknown; until?: unknown; order?: unknown }):
  { since: string | undefined; until: string | undefined; lo?: string; hi?: string; order: "newest" | "oldest" } | string {
  const bad = (name: string) => `${name} must be a date as YYYY, YYYY-MM or YYYY-MM-DD (e.g. "2025" or "2025-05-16")`;
  const present = (v: unknown) => v !== undefined && v !== null && v !== "";
  let lo: string | undefined;
  let hi: string | undefined;
  if (present(args.since)) {
    lo = dateBound(args.since, false) ?? undefined;
    if (!lo) return bad("since");
  }
  if (present(args.until)) {
    hi = dateBound(args.until, true) ?? undefined;
    if (!hi) return bad("until");
  }
  if (lo && hi && lo > hi) return "since is after until";
  const order = args.order ?? "newest";
  if (order !== "newest" && order !== "oldest") return 'order must be "newest" or "oldest"';
  return { since: present(args.since) ? String(args.since) : undefined, until: present(args.until) ? String(args.until) : undefined, lo, hi, order };
}

const LIST_MAX = 100;

export async function listLegislation(
  db: D1Database,
  args: { since?: string; until?: string; kind?: string; order?: string; offset?: number; limit?: number }
): Promise<ToolResult> {
  const r = listRange(args);
  if (typeof r === "string") return err(r);
  const limit = clampInt(args.limit, 50, 1, LIST_MAX);
  const offset = clampInt(args.offset, 0, 0, 1_000_000);
  const kind = typeof args.kind === "string" ? args.kind.trim() : "";

  const dateExpr = "coalesce(nullif(gazette_date, ''), issued_on)";
  const where: string[] = [`${dateExpr} IS NOT NULL`];
  const bind: unknown[] = [];
  if (r.lo) { where.push(`${dateExpr} >= ?`); bind.push(r.lo); }
  if (r.hi) { where.push(`${dateExpr} <= ?`); bind.push(r.hi); }
  if (kind !== "") { where.push("(kind = ? OR lower(kind_en) = lower(?))"); bind.push(kind, kind); }
  const w = where.join(" AND ");
  const dir = r.order === "newest" ? "DESC" : "ASC";

  const total = (await db.prepare(`SELECT count(*) AS n FROM legislation WHERE ${w}`).bind(...bind).first<{ n: number }>())?.n ?? 0;
  const rows = (await db.prepare(`SELECT *, ${dateExpr} AS list_date FROM legislation WHERE ${w} ORDER BY list_date ${dir}, id ${dir} LIMIT ? OFFSET ?`)
    .bind(...bind, limit, offset).all<LegislationRow & { list_date: string }>()).results;
  const next = offset + rows.length;
  return ok({
    since: r.since,
    until: r.until,
    kind: kind || undefined,
    order: r.order,
    total,
    offset,
    next_offset: next < total ? next : undefined,
    laws: rows.map((l) => ({
      ...lawSummary(l),
      gazette_number: l.gazette_number ?? undefined,
      gazette_date: l.gazette_date ?? undefined,
      gazette_issue_id: l.gazette_issue_id ?? undefined,
      date: l.list_date,
    })),
  });
}

export async function listGazetteIssues(
  db: D1Database,
  args: { since?: string; until?: string; order?: string; offset?: number; limit?: number }
): Promise<ToolResult> {
  const r = listRange(args);
  if (typeof r === "string") return err(r);
  const limit = clampInt(args.limit, 50, 1, LIST_MAX);
  const offset = clampInt(args.offset, 0, 0, 1_000_000);

  const where: string[] = ["g.published_on IS NOT NULL"];
  const bind: unknown[] = [];
  if (r.lo) { where.push("g.published_on >= ?"); bind.push(r.lo); }
  if (r.hi) { where.push("g.published_on <= ?"); bind.push(r.hi); }
  const w = where.join(" AND ");
  const dir = r.order === "newest" ? "DESC" : "ASC";

  const total = (await db.prepare(`SELECT count(*) AS n FROM gazette_issue g WHERE ${w}`).bind(...bind).first<{ n: number }>())?.n ?? 0;
  const rows = (await db.prepare(
    `SELECT g.id, g.number, g.published_on, (SELECT count(*) FROM gazette_entry e WHERE e.issue_id = g.id) AS text_count
     FROM gazette_issue g WHERE ${w} ORDER BY g.published_on ${dir}, g.id ${dir} LIMIT ? OFFSET ?`
  ).bind(...bind, limit, offset).all<{ id: number; number: string | null; published_on: string; text_count: number }>()).results;
  const next = offset + rows.length;
  return ok({
    since: r.since,
    until: r.until,
    order: r.order,
    total,
    offset,
    next_offset: next < total ? next : undefined,
    issues: rows.map((g) => ({
      id: g.id,
      number: g.number ?? undefined,
      published_on: g.published_on,
      text_count: g.text_count,
      source: g.id >= 2_000_000_000 ? "scans" : "centre",
      url: `${BASE}/gazette/${g.id}/`,
    })),
  });
}

// ---------------------------------------------------------------- possibly_affected

export async function possiblyAffected(db: D1Database, args: { law_id?: number }): Promise<ToolResult> {
  const lawID = args.law_id;
  if (!lawID || lawID <= 0) return err("law_id must be a positive integer");
  const target = await loadLaw(db, lawID);
  if (!target) return err(`law ${lawID} not found`);
  const rows = await db.prepare(
    `SELECT p.repealing_article, p.article_number, l.* FROM possibly_affected p
     JOIN legislation l ON l.id = p.repealing_law_id
     WHERE p.law_id = ? ORDER BY coalesce(l.gazette_date, l.issued_on)`
  ).bind(lawID).all<LegislationRow & { repealing_article: string | null; article_number: string | null }>();
  return ok({
    law: lawSummary(target),
    possibly_affected_by: rows.results.map((r) => ({
      by: lawSummary(r),
      by_article: r.repealing_article ?? "",
      article_number: r.article_number ?? undefined,
      url: r.article_number ? articleURL(lawID, r.article_number) : `${BASE}/law/${lawID}/`,
    })),
  });
}
