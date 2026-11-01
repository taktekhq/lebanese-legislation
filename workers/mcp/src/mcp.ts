// The MCP JSON-RPC 2.0 layer: initialize, notifications/initialized, ping,
// tools/list, tools/call. Mirrors internal/site/mcp.go (the Go draft,
// never wired in): same protocol versions, same tool names and
// descriptions, same not-legal-advice wrapping, so a client sees the same
// server whichever side answers it from.

import { NOT_LEGAL_ADVICE } from "./db";
import {
  getArticle, getGazetteIssue, getLaw, getLawTexts, getLinks, getRuling, listGazetteIssues, listLegislation, possiblyAffected, searchLegislation, ToolResult,
} from "./tools";

export const DEFAULT_PROTOCOL = "2025-06-18";
const SUPPORTED_PROTOCOLS = new Set(["2025-06-18", "2025-03-26"]);

export interface RpcRequest {
  jsonrpc?: string;
  id?: unknown;
  method?: string;
  params?: unknown;
}

export interface RpcResponse {
  jsonrpc: "2.0";
  id?: unknown;
  result?: unknown;
  error?: { code: number; message: string };
}

export const RPC_PARSE_ERROR = -32700;
export const RPC_INVALID_REQUEST = -32600;
export const RPC_METHOD_NOT_FOUND = -32601;
export const RPC_INVALID_PARAMS = -32602;
export const RPC_INTERNAL_ERROR = -32603;

export function rpcError(id: unknown, code: number, message: string): RpcResponse {
  return { jsonrpc: "2.0", id, error: { code, message } };
}
export function rpcResult(id: unknown, result: unknown): RpcResponse {
  return { jsonrpc: "2.0", id, result };
}

// isNotification: a JSON-RPC request with no "id" field at all (not even
// null) is a notification and must get no response body, only the HTTP
// status; `"id" in req` (not req.id === undefined) is what tells the two
// apart, since an explicit `"id": null` is still a request.
export function isNotification(req: RpcRequest): boolean {
  return !("id" in req);
}

const CALCULATED_CURRENT_GUIDANCE =
  "When calculated_current is present, use its text_ar for current monetary amounts and cite its authority.url. " +
  "Identify this as calculated under the authority, never as verbatim published wording. " +
  "The article text_ar and source fields remain published wording. ";

export const toolDefs = [
  {
    name: "search_legislation",
    description: CALCULATED_CURRENT_GUIDANCE +
      "Search Lebanese legislation (laws, decrees, decisions), their articles, court rulings, or Official " +
      "Gazette issues, by keyword: Arabic words, French words (articles the Centre published in French, and " +
      "the codes' French or English names), or a law's numeric id. Returns verbatim snippets from the Lebanese University's " +
      "database, never an AI-written summary. A ruling hit's pdf_url is its full judgment's scan on the " +
      "Lebanese University's site: always pass it on with any answer that cites the ruling. English words work too: " +
      "they are read into Arabic through a glossary, matched against the unofficial English titles, and when nothing " +
      "matches the result says what was searched instead (searched_instead). A code named in the query (\"قانون العقوبات 670\", " +
      "\"أصول محاكمات مدنية صورية\") narrows the articles to that code; when none of its articles holds the other words, " +
      "articles_not_in_code names it and the articles listed are from other texts. Each law carries title_en, an unofficial " +
      "English title a model wrote from the Arabic title alone: the Arabic title and text are official and more accurate; " +
      "cite the Arabic. A search of the gazette category with a bare year (\"2025\") lists that year's issues, and \"22/2025\" is " +
      "issue 22 of 2025; to list texts by date use list_legislation instead. Not legal advice.",
    inputSchema: {
      type: "object",
      properties: {
        query: { type: "string", description: "words to search for, Arabic, English or French, or a law's id" },
        kind: { type: "string", enum: ["laws", "articles", "rulings", "gazette"], description: "narrow to one category; omit to search all four" },
        year: { type: "integer", description: "only laws issued this year; for the gazette category, only issues published this year" },
        limit: { type: "integer", minimum: 1, maximum: 20, default: 10, description: "results per category, at most 20" },
      },
      required: ["query"],
    },
  },
  {
    name: "get_law",
    description:
      "Get a law, decree or similar text's metadata (kind, number, date, status), its table of contents (the " +
      "chapters: id, parent_id, depth, heading), its articles in order (number, and the section_id of the chapter " +
      "each sits under) and the texts it amended or was amended by (amended_by / amended: one entry per text and relation, " +
      "with articles listing the articles of this law it touches, or of the other law for amended; no articles means the whole law). Use get_article to read one article's verbatim " +
      "text, or get_law_texts to read all of them. title_en is an unofficial English title (a model's rendering of " +
      "the Arabic one); the Arabic title and text are official and more accurate. Not legal advice.",
    inputSchema: {
      type: "object",
      properties: { id: { type: "integer", description: "the law's numeric id, from search_legislation or its URL" } },
      required: ["id"],
    },
  },
  {
    name: "get_article",
    description: CALCULATED_CURRENT_GUIDANCE +
      "Get one article's verbatim Arabic text as in force today (and French, when the Centre published it and no " +
      "amendment has replaced it), its in-force status, amendment history, and the canonical and source URLs. When an " +
      "amendment replaced the wording, text_ar is the new wording and centre_text_ar the earlier one the Centre's site " +
      "still shows. centre_error, when present, notes an error in the Centre's own text (one article repeating " +
      "another, a stray letter); the text itself is left as published. The text is copied exactly from the " +
      "database; nothing here is AI-written. Laws with ids from " +
      "2000000000 are ones the Lebanese University has not published yet, read by machine from scans of the Official " +
      "Gazette's pages (unofficial copies, page images on the law's page); where the two readings differ the text " +
      "shows both as ⟦reading one | reading two⟧: quote neither as certain. Not legal advice.",
    inputSchema: {
      type: "object",
      properties: {
        law_id: { type: "integer", description: "the law's id (see get_law)" },
        number: { type: "string", description: "the article's printed number, e.g. \"212\", \"12 مكرر\" or \"586-1\" (an article numbered under another; \"586 (1)\" works too)" },
      },
      required: ["law_id", "number"],
    },
  },
  {
    name: "get_law_texts",
    description: CALCULATED_CURRENT_GUIDANCE +
      "Get the verbatim texts of a law's articles in order, a page at a time: for each article its number, " +
      "section_id, note, and the same text fields as get_article (text_ar in force today, text_fr, centre_text_ar " +
      "when an amendment replaced the wording, version, repealed). Follow next_offset until it is absent to read " +
      "the whole law. Amendment history stays in get_article. Not legal advice.",
    inputSchema: {
      type: "object",
      properties: {
        law_id: { type: "integer", description: "the law's numeric id" },
        offset: { type: "integer", minimum: 0, default: 0, description: "how many articles to skip (next_offset of the previous page)" },
        limit: { type: "integer", minimum: 1, maximum: 200, default: 50, description: "articles per page, at most 200" },
      },
      required: ["law_id"],
    },
  },
  {
    name: "get_ruling",
    description:
      "Get one court ruling (judgment) by id: court, number, date, panel, and its verbatim text when the Centre " +
      "holds it as typed text. When available, pages returns the scanned judgment's machine reading, with " +
      "low_confidence: true on hard-to-read pages where errors are expected. pdf_url is the full judgment's scan on " +
      "the Lebanese University's site: always give it to the user with any answer about the ruling, so they can " +
      "open the original. Not legal advice.",
    inputSchema: {
      type: "object",
      properties: { id: { type: "integer", description: "the ruling's numeric id, from search_legislation" } },
      required: ["id"],
    },
  },
  {
    name: "get_gazette_issue",
    description:
      "Get one Official Gazette issue by id: its number, publication date, and the laws, decrees and decisions it published. Not legal advice.",
    inputSchema: {
      type: "object",
      properties: { id: { type: "integer", description: "the issue's numeric id, from search_legislation" } },
      required: ["id"],
    },
  },
  {
    name: "get_links",
    description:
      "Get the graph edges into or out of one item: a law, an article, a ruling, an opinion, a research study or " +
      "a Gazette issue. Use this to walk citations and amendments directly, instead of only what get_law and " +
      "get_article attach automatically. For a law key (\"law/<id>\") the default scope \"all\" includes the edges of its " +
      "articles too (each link keeps its real to_key, e.g. \"law/244611/art/37\"); scope \"law\" is the law itself only. " +
      "Newest first by the date of the text each edge comes from; page with offset and next_offset, total is the " +
      "number of edges. Not legal advice.",
    inputSchema: {
      type: "object",
      properties: {
        key: { type: "string", description: "the item's key: \"law/<id>\", \"law/<id>/art/<number>\", \"ruling/<id>\", \"opinion/<id>\", \"research/<id>\" or \"gazette/<id>\"" },
        direction: { type: "string", enum: ["to", "from"], default: "to", description: "\"to\" (edges that name this item as their target) or \"from\" (edges this item itself carries)" },
        relation: { type: "string", description: "narrow to one relation (e.g. \"cites\", \"amend\", \"repeal\"); omit for all" },
        scope: { type: "string", enum: ["all", "law"], description: "for a law key only: \"all\" (default) adds the edges to or from its articles, \"law\" is the law itself only" },
        order: { type: "string", enum: ["newest", "oldest"], default: "newest", description: "by the date of the text the edge comes from (Gazette date, else issue date)" },
        offset: { type: "integer", minimum: 0, default: 0, description: "how many edges to skip (next_offset of the previous page)" },
        limit: { type: "integer", minimum: 1, maximum: 200, default: 50, description: "at most this many edges. quote is cut to 600 characters when longer (quote_truncated: true); get_article has the full wording" },
      },
      required: ["key"],
    },
  },
  {
    name: "list_legislation",
    description:
      "List laws, decrees and decisions by date, newest first: for \"everything published since January 2025\" call " +
      "with since \"2025-01\". The date is the publication date: the Official Gazette date, else the issue date " +
      "(each entry's date, with gazette_number, gazette_date and gazette_issue_id). Recent texts come from machine " +
      "readings of Gazette scans (ids from 2000000000, unofficial) because the Lebanese University's Centre lags " +
      "about a year, so a recent period lists mostly those. since and until accept YYYY, YYYY-MM or YYYY-MM-DD, " +
      "inclusive. kind narrows to one kind, in Arabic (\"قانون\", \"مرسوم\") or English (\"Law\", \"Decree\"). " +
      "Page with offset and next_offset; total is the whole count. Not legal advice.",
    inputSchema: {
      type: "object",
      properties: {
        since: { type: "string", description: "first date, inclusive: YYYY, YYYY-MM or YYYY-MM-DD (e.g. \"2025-01\")" },
        until: { type: "string", description: "last date, inclusive: YYYY, YYYY-MM or YYYY-MM-DD" },
        kind: { type: "string", description: "the kind of text, Arabic or English (e.g. \"قانون\" or \"Law\", \"Decree\")" },
        order: { type: "string", enum: ["newest", "oldest"], default: "newest", description: "by publication date" },
        offset: { type: "integer", minimum: 0, default: 0, description: "how many texts to skip (next_offset of the previous page)" },
        limit: { type: "integer", minimum: 1, maximum: 100, default: 50, description: "texts per page, at most 100" },
      },
    },
  },
  {
    name: "list_gazette_issues",
    description:
      "List Official Gazette issues by publication date, newest first, with the number of texts each carries " +
      "(text_count) and where the issue comes from: source \"centre\" (the Lebanese University's Centre, whose issues " +
      "stop about a year back) or \"scans\" (issues seen in Gazette scans, ids from 2000000000, which cover recent " +
      "months). since and until accept YYYY, YYYY-MM or YYYY-MM-DD, inclusive; \"everything since January 2025\" is " +
      "since \"2025-01\". Page with offset and next_offset. get_gazette_issue reads one issue's texts. Not legal advice.",
    inputSchema: {
      type: "object",
      properties: {
        since: { type: "string", description: "first date, inclusive: YYYY, YYYY-MM or YYYY-MM-DD" },
        until: { type: "string", description: "last date, inclusive: YYYY, YYYY-MM or YYYY-MM-DD" },
        order: { type: "string", enum: ["newest", "oldest"], default: "newest", description: "by publication date" },
        offset: { type: "integer", minimum: 0, default: 0, description: "how many issues to skip (next_offset of the previous page)" },
        limit: { type: "integer", minimum: 1, maximum: 100, default: 50, description: "issues per page, at most 100" },
      },
    },
  },
  {
    name: "possibly_affected",
    description:
      "Get the general repeals that may touch a law: earlier texts with a \"all provisions contrary to this law " +
      "are repealed\" clause pointing at it. A list to check, never a finding that anything was actually repealed. Not legal advice.",
    inputSchema: {
      type: "object",
      properties: { law_id: { type: "integer", description: "the law's numeric id" } },
      required: ["law_id"],
    },
  },
] as const;

const toolNames: Set<string> = new Set(toolDefs.map((t) => t.name));

export function mcpInitialize(params: unknown): Record<string, unknown> {
  let version = DEFAULT_PROTOCOL;
  if (params && typeof params === "object" && "protocolVersion" in params) {
    const pv = (params as { protocolVersion?: unknown }).protocolVersion;
    if (typeof pv === "string" && SUPPORTED_PROTOCOLS.has(pv)) version = pv;
  }
  return {
    protocolVersion: version,
    serverInfo: { name: "lebaneselegislation", version: "1.1.0" },
    capabilities: { tools: {} },
    instructions:
      "Lebanese Legislation: every law, article, court ruling and Official Gazette issue published by the " +
      "Lebanese University's Centre for Research and Studies in Legal Informatics. Published source text is copied " +
      "verbatim from that database; nothing here is written or rewritten by a model. " + CALCULATED_CURRENT_GUIDANCE + NOT_LEGAL_ADVICE,
  };
}

export async function callTool(db: D1Database, name: string, args: Record<string, unknown>): Promise<ToolResult> {
  switch (name) {
    case "search_legislation":
      return searchLegislation(db, args);
    case "get_law":
      return getLaw(db, args);
    case "get_article":
      return getArticle(db, args);
    case "get_law_texts":
      return getLawTexts(db, args);
    case "get_ruling":
      return getRuling(db, args);
    case "get_gazette_issue":
      return getGazetteIssue(db, args);
    case "get_links":
      return getLinks(db, args);
    case "list_legislation":
      return listLegislation(db, args);
    case "list_gazette_issues":
      return listGazetteIssues(db, args);
    case "possibly_affected":
      return possiblyAffected(db, args);
    default:
      return { body: { error: "unknown tool" }, isError: true };
  }
}

export function isKnownTool(name: string): boolean {
  return toolNames.has(name);
}
