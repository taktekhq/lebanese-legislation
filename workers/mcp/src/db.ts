// Row shapes and small helpers shared by the tools (src/tools.ts). Mirrors
// internal/site/api.go's shapes (apiLawSummary, apiArticleDetail, ...) so
// an MCP client sees the same fields the HTTP API and the (not wired)
// Go MCP draft would have returned, from the same database by a different
// road: Postgres on Hetzner at build time, D1 here at request time.

export const BASE = "https://lebaneselegislation.com";

export const NOT_LEGAL_ADVICE =
  "Not legal advice. Official texts from the Lebanese University's Centre for Research and Studies in Legal Informatics.";

export interface LegislationRow {
  id: number;
  kind: string;
  kind_en: string | null;
  number: string | null;
  issued_on: string | null;
  title: string;
  // Unofficial English title (a model's rendering of the Arabic one); null when none. Arabic is official.
  title_en?: string | null;
  status: string;
  gazette_number: string | null;
  gazette_date: string | null;
  gazette_issue_id: number | null;
  article_count: number;
  code_slug: string | null;
  code_name_en: string | null;
  source_url: string | null;
  // The day the text's page last changed on the site (page_state), never the
  // text's own date. Absent on a snapshot older than the column.
  updated_at?: string | null;
}

export interface LawSummary {
  id: number;
  kind: string;
  kind_en?: string;
  number?: string;
  issued_on?: string;
  title: string;
  title_en?: string; // unofficial English title; the Arabic title is the official, more accurate one
  status: string;
  article_count: number;
  code?: string;
  url: string;
  source_url?: string;
  updated_at?: string;
}

export function lawSummary(l: LegislationRow): LawSummary {
  const out: LawSummary = {
    id: l.id,
    kind: l.kind,
    title: l.title,
    status: l.status,
    article_count: l.article_count,
    url: lawURL(l.id, l.kind),
  };
  if (l.title_en) out.title_en = l.title_en;
  if (l.kind_en) out.kind_en = l.kind_en;
  if (l.number) out.number = l.number;
  if (l.issued_on) out.issued_on = l.issued_on;
  if (l.code_name_en) out.code = l.code_name_en;
  if (l.source_url) out.source_url = l.source_url;
  if (l.updated_at) out.updated_at = l.updated_at;
  return out;
}

// slug turns an article's printed number into the same path segment
// law.html / api.go build (Slug in internal/site), close enough for an
// article whose number is plain digits or "N مكرر"; non-ASCII characters
// are percent-encoded by encodeArticlePath below rather than transliterated,
// which keeps the URL correct even if the exact slugging rule drifts.
export function articleURL(lawID: number, number: string, kind?: string): string {
  return `${lawURL(lawID, kind)}article/${encodeURIComponent(number.trim())}/`;
}

// wordForKind is the word in a text's address on the site, as
// internal/site/words.go chooses it: /law/ for laws and the constitution,
// /decree/ for decrees, /text/ for every other kind. Without the kind it
// is /law/, which the site redirects to the right word.
export function wordForKind(kind?: string): string {
  if (!kind) return "law";
  if (kind.startsWith("مرسوم")) return "decree";
  if (kind.startsWith("قانون") || kind === "الدستور" || kind === "دستور") return "law";
  return "text";
}

export function lawURL(id: number, kind?: string): string {
  return `${BASE}/${wordForKind(kind)}/${id}/`;
}

export function withAdvice<T>(result: T): { result: T; not_legal_advice: string } {
  return { result, not_legal_advice: NOT_LEGAL_ADVICE };
}

export function toolError(message: string): { error: string } {
  return { error: message };
}

// clampInt mirrors clampInt in internal/site/api.go.
export function clampInt(v: unknown, def: number, lo: number, hi: number): number {
  const n = typeof v === "number" ? Math.trunc(v) : Number.NaN;
  if (!Number.isFinite(n)) return def;
  return Math.max(lo, Math.min(hi, n));
}
