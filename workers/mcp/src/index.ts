// The Worker entry point: HTTP transport for /mcp (CORS, rate limiting,
// body size cap, a short result cache, a per-call timeout) around the
// JSON-RPC layer in src/mcp.ts. Stateless: every request reads the D1
// snapshot bound as env.DB and nothing is written back, so MCP traffic
// never reaches Hetzner Postgres (see internal/site/mcp.go's package doc
// for why this moved here instead of being wired into the Go server).

import {
  callTool, DEFAULT_PROTOCOL, isKnownTool, isNotification, mcpInitialize, rpcError, rpcResult, RpcRequest, RpcResponse,
  RPC_INTERNAL_ERROR, RPC_INVALID_PARAMS, RPC_INVALID_REQUEST, RPC_METHOD_NOT_FOUND, RPC_PARSE_ERROR, toolDefs,
} from "./mcp";
import { handleBadge } from "./badge";
import { clip, GAEvent, mcpStream, track } from "./ga";
import { phCapture, visitorId } from "./posthog";
import { hfStatsEvents } from "./hfstats";
import { clientKind } from "./clientkind";

const MAX_BODY = 64 * 1024; // request body cap
const CALL_TIMEOUT_MS = 5000; // per tools/call budget, mirrors mcpCallTimeout in the Go draft
const CACHE_TTL_SECONDS = 60; // identical tool calls, briefly (Cache API, see handleToolsCall)

function corsHeaders(): HeadersInit {
  return {
    "Access-Control-Allow-Origin": "*",
    "Access-Control-Allow-Methods": "POST, GET, OPTIONS",
    "Access-Control-Allow-Headers": "Content-Type, Mcp-Session-Id, Mcp-Protocol-Version, Authorization",
    "Access-Control-Expose-Headers": "Mcp-Session-Id, Mcp-Protocol-Version",
  };
}

function jsonResponse(status: number, body: unknown, extraHeaders: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Mcp-Protocol-Version": DEFAULT_PROTOCOL,
      ...corsHeaders(),
      ...extraHeaders,
    },
  });
}

// readBodyCapped reads the request body up to `cap` bytes; returns null if
// the body is larger, without buffering the excess.
async function readBodyCapped(request: Request, cap: number): Promise<string | null> {
  if (!request.body) return "";
  const reader = request.body.getReader();
  let received = 0;
  const chunks: Uint8Array[] = [];
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    received += value.byteLength;
    if (received > cap) {
      await reader.cancel();
      return null;
    }
    chunks.push(value);
  }
  const buf = new Uint8Array(received);
  let offset = 0;
  for (const c of chunks) {
    buf.set(c, offset);
    offset += c.byteLength;
  }
  return new TextDecoder().decode(buf);
}

function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise((resolve, reject) => {
    const t = setTimeout(() => reject(new Error("tool call timed out")), ms);
    p.then(
      (v) => { clearTimeout(t); resolve(v); },
      (e) => { clearTimeout(t); reject(e); },
    );
  });
}

function cacheKeyRequest(origin: string, name: string, argsJSON: string): Request {
  const u = new URL(origin);
  u.pathname = "/__mcp_cache__/" + encodeURIComponent(name);
  u.search = "?a=" + encodeURIComponent(argsJSON);
  return new Request(u.toString(), { method: "GET" });
}

// MCP_PAGE is the page_location every MCP event reports in GA4, so the
// endpoint shows up as one page beside the site's own.
const MCP_PAGE = "https://lebaneselegislation.com/mcp";

// What one JSON-RPC request did, for PostHog ($mcp_* properties, the shape
// PostHog's MCP analytics screens read). Filled in by handleRPC.
interface CallRecord {
  event?: "$mcp_tool_call" | "$mcp_initialize" | "mcp_tools_list";
  props: Record<string, unknown>;
}

const SERVER_PROPS = { $mcp_server_name: "lebaneselegislation", $mcp_server_version: "1.1.0", $mcp_source: "lebaneselegislation_worker" };

// logRefusal sends PostHog an mcp_rate_limited event for a request the
// per-IP limit refused, about one per IP per minute (MCP_REFUSAL_LOG), so
// we see who hits the limit without a flood of refusals becoming a flood of
// events. Cloudflare's limiters count approximately, per location: on
// 24 Oct 2026, 126 refusals in 5 s from one IP logged 6 events.
async function logRefusal(env: Env, ctx: ExecutionContext | undefined, request: Request, ip: string): Promise<void> {
  if (!ctx || !env.POSTHOG_KEY) return;
  if (env.MCP_REFUSAL_LOG && !(await env.MCP_REFUSAL_LOG.limit({ key: ip })).success) return;
  const ua = request.headers.get("User-Agent") ?? "";
  const props: Record<string, unknown> = {
    ...SERVER_PROPS,
    $current_url: MCP_PAGE,
    $user_agent: ua,
    $geoip_country_code: (request as unknown as { cf?: { country?: string } }).cf?.country,
    client_kind: clientKind(ua, undefined),
    limit_per_minute: 60,
  };
  if (props.client_kind === "monitor") props.$process_person_profile = false;
  phCapture(env, ctx, request, [{ event: "mcp_rate_limited", properties: props, distinctId: await visitorId(env, ip, ua) }]);
}

// mcpErrorType buckets a thrown tools/call failure the way PostHog's MCP
// analytics does; tool-level errors (bad arguments, not found) are
// "validation" and set where they happen.
export function mcpErrorType(e: unknown): "timeout" | "internal" {
  return e instanceof Error && /timed out/.test(e.message) ? "timeout" : "internal";
}

// mcpSessionId groups one machine's calls on one UTC day into a session: a
// UUIDv7 (PostHog's session id shape) whose time is the day's start and
// whose random bits come from the visitor id.
export function mcpSessionId(visitor: string, day: string): string {
  const ms = Date.parse(day + "T00:00:00Z").toString(16).padStart(12, "0");
  const h = visitor.padEnd(32, "0");
  const variant = ((parseInt(h[16], 16) & 0x3) | 0x8).toString(16);
  return `${ms.slice(0, 8)}-${ms.slice(8, 12)}-7${h.slice(0, 3)}-${variant}${h.slice(17, 20)}-${h.slice(20, 32)}`;
}

// resultCount adds up a tool result's *_count fields (searches), or counts
// it when it is an array (lists); undefined when it has neither. Tool bodies
// arrive wrapped as { result, not_legal_advice } (db.ts withAdvice).
function resultCount(body: unknown): number | undefined {
  if (body && typeof body === "object" && "result" in body) body = (body as { result: unknown }).result;
  if (Array.isArray(body)) return body.length;
  if (!body || typeof body !== "object") return undefined;
  const counts = Object.entries(body as Record<string, unknown>).filter(([k, v]) => k.endsWith("_count") && typeof v === "number");
  return counts.length ? counts.reduce((n, [, v]) => n + (v as number), 0) : undefined;
}

// toolCallEvent describes one tools/call for GA4: which tool, whether it
// came from the cache or failed, and for searches what was searched.
function toolCallEvent(name: string, args: Record<string, unknown>, cacheHit: boolean, isError: boolean): GAEvent {
  const str = (v: unknown) => (typeof v === "string" ? v : undefined);
  return {
    name: "mcp_tool_call",
    params: {
      tool_name: name,
      cache_hit: cacheHit ? 1 : 0,
      is_error: isError ? 1 : 0,
      search_term: name === "search_legislation" ? str(args.query) : undefined,
      search_kind: name === "search_legislation" ? str(args.kind) : undefined,
    },
  };
}

async function handleToolsCall(db: D1Database, req: RpcRequest, requestURL: string, events: GAEvent[], rec: CallRecord): Promise<[RpcResponse, number]> {
  const params = (req.params ?? {}) as { name?: string; arguments?: unknown };
  const name = params.name;
  if (!name) return [rpcError(req.id, RPC_INVALID_PARAMS, "tools/call needs a tool name"), 200];
  const args = (params.arguments ?? {}) as Record<string, unknown>;
  rec.event = "$mcp_tool_call";
  Object.assign(rec.props, { $mcp_tool_name: name, $mcp_parameters: args, $mcp_cache_hit: false, $mcp_is_error: false });
  if (!isKnownTool(name)) {
    Object.assign(rec.props, { $mcp_is_error: true, $mcp_error_type: "validation" });
    events.push(toolCallEvent(name, {}, false, true));
    return [rpcError(req.id, RPC_INVALID_PARAMS, "unknown tool " + name), 200];
  }
  const argsJSON = JSON.stringify(args);

  const cache = (caches as unknown as WorkersCacheStorage).default;
  const cacheKey = cacheKeyRequest(requestURL, name, argsJSON);
  const hit = await cache.match(cacheKey);
  if (hit) {
    events.push(toolCallEvent(name, args, true, false));
    const cachedResult = (await hit.json()) as { content?: Array<{ text?: string }> };
    const text = cachedResult.content?.[0]?.text ?? "";
    Object.assign(rec.props, { $mcp_cache_hit: true, $mcp_response: text });
    try { rec.props.$mcp_result_count = resultCount(JSON.parse(text)); } catch { /* not JSON */ }
    return [rpcResult(req.id, cachedResult), 200];
  }

  let result;
  try {
    result = await withTimeout(callTool(db, name, args), CALL_TIMEOUT_MS);
  } catch (e) {
    console.error(`mcp tools/call ${name}:`, e);
    Object.assign(rec.props, { $mcp_is_error: true, $mcp_error_type: mcpErrorType(e), $mcp_error_message: String(e) });
    events.push(toolCallEvent(name, args, false, true));
    return [rpcError(req.id, RPC_INTERNAL_ERROR, "internal error"), 200];
  }
  events.push(toolCallEvent(name, args, false, result.isError));
  const toolResult = { content: [{ type: "text", text: JSON.stringify(result.body) }], isError: result.isError };
  Object.assign(rec.props, { $mcp_response: toolResult.content[0].text, $mcp_result_count: resultCount(result.body) });
  if (result.isError) Object.assign(rec.props, { $mcp_is_error: true, $mcp_error_type: "validation" });
  if (!result.isError) {
    const cached = new Response(JSON.stringify(toolResult), {
      headers: { "Content-Type": "application/json", "Cache-Control": `public, max-age=${CACHE_TTL_SECONDS}` },
    });
    await cache.put(cacheKey, cached);
  }
  return [rpcResult(req.id, toolResult), 200];
}

// initializeEvent records which MCP client connected (its clientInfo).
function initializeEvent(params: unknown): GAEvent {
  const p = (params ?? {}) as { protocolVersion?: unknown; clientInfo?: { name?: unknown; version?: unknown } };
  const str = (v: unknown) => (typeof v === "string" ? v : undefined);
  return {
    name: "mcp_initialize",
    params: { client_name: str(p.clientInfo?.name), client_version: str(p.clientInfo?.version), protocol_version: str(p.protocolVersion) },
  };
}

// handleRPC answers one JSON-RPC request and appends what happened to
// `events`, for GA4.
async function handleRPC(db: D1Database, req: RpcRequest, requestURL: string, events: GAEvent[], rec: CallRecord): Promise<[RpcResponse, number]> {
  switch (req.method) {
    case "initialize": {
      const ev = initializeEvent(req.params);
      events.push(ev);
      rec.event = "$mcp_initialize";
      Object.assign(rec.props, {
        $mcp_client_name: ev.params.client_name, $mcp_client_version: ev.params.client_version, $mcp_protocol_version: ev.params.protocol_version,
      });
      return [rpcResult(req.id, mcpInitialize(req.params)), 200];
    }
    case "ping":
      return [rpcResult(req.id, {}), 200];
    case "tools/list":
      events.push({ name: "mcp_tools_list", params: {} });
      rec.event = "mcp_tools_list";
      rec.props.$mcp_listed_tool_names = toolDefs.map((t) => t.name);
      return [rpcResult(req.id, { tools: toolDefs }), 200];
    case "tools/call":
      return handleToolsCall(db, req, requestURL, events, rec);
    default:
      return [rpcError(req.id, RPC_METHOD_NOT_FOUND, "unknown method " + req.method), 200];
  }
}

export default {
  async fetch(request: Request, env: Env, ctx?: ExecutionContext): Promise<Response> {
    if (request.method === "OPTIONS") {
      return new Response(null, { status: 204, headers: corsHeaders() });
    }

    const url = new URL(request.url);
    if (url.pathname.startsWith("/badge/")) {
      return handleBadge(request, url, env, ctx);
    }
    if (url.pathname !== "/mcp" && url.pathname !== "/mcp/") {
      return jsonResponse(404, { error: "not found", hint: "this Worker only serves /mcp" });
    }

    if (request.method === "GET") {
      // A person who clicks the address in a post gets the setup page, not
      // JSON; MCP clients probing for SSE send Accept: text/event-stream.
      const accept = request.headers.get("Accept") ?? "";
      if (accept.includes("text/html") && !accept.includes("text/event-stream")) {
        return Response.redirect("https://lebaneselegislation.com/ai/", 302);
      }
      return jsonResponse(
        405,
        { error: "this MCP server is stateless: POST a JSON-RPC 2.0 request, there is no SSE stream to open", see: "https://lebaneselegislation.com/ai/" },
        { Allow: "POST, OPTIONS" },
      );
    }
    if (request.method !== "POST") {
      return jsonResponse(405, { error: "method not allowed" }, { Allow: "POST, OPTIONS" });
    }

    const ip = request.headers.get("CF-Connecting-IP") ?? request.headers.get("X-Forwarded-For") ?? "unknown";
    const rl = await env.MCP_RATE_LIMITER.limit({ key: ip });
    if (!rl.success) {
      await logRefusal(env, ctx, request, ip);
      return jsonResponse(429, { error: "too many requests, slow down" }, { "Retry-After": "2" });
    }

    const bodyText = await readBodyCapped(request, MAX_BODY);
    if (bodyText === null) {
      return jsonResponse(413, { error: "request body too large" });
    }

    let parsed: unknown;
    try {
      parsed = bodyText === "" ? {} : JSON.parse(bodyText);
    } catch {
      return jsonResponse(400, rpcError(null, RPC_PARSE_ERROR, "invalid JSON"));
    }
    const req = parsed as RpcRequest;
    if (req.jsonrpc !== "2.0" || !req.method) {
      return jsonResponse(400, rpcError(req.id, RPC_INVALID_REQUEST, "not a JSON-RPC 2.0 request"));
    }

    if (isNotification(req)) {
      return new Response(null, { status: 202, headers: corsHeaders() });
    }

    const events: GAEvent[] = [];
    const rec: CallRecord = { props: {} };
    const started = Date.now();
    const [resp, status] = await handleRPC(env.DB, req, request.url, events, rec);
    const ua = clip(request.headers.get("User-Agent") ?? "");
    for (const e of events) Object.assign(e.params, { page_location: MCP_PAGE, page_title: "MCP server", client_ua: ua });
    track(mcpStream(env), ctx, request, events);
    if (rec.event && ctx && env.POSTHOG_KEY) {
      const fullUA = request.headers.get("User-Agent") ?? "";
      const visitor = await visitorId(env, ip, fullUA);
      const country = (request as unknown as { cf?: { country?: string } }).cf?.country;
      const props: Record<string, unknown> = {
        $mcp_client_name: fullUA || undefined,
        ...rec.props,
        ...SERVER_PROPS,
        $mcp_duration_ms: Date.now() - started,
        $session_id: mcpSessionId(visitor, new Date().toISOString().slice(0, 10)),
        $current_url: MCP_PAGE,
        $user_agent: fullUA,
        $geoip_country_code: country,
        jsonrpc_method: req.method,
      };
      // Monitoring bots stay countable but out of the people list.
      props.client_kind = clientKind(fullUA, typeof rec.props.$mcp_client_name === "string" && req.method === "initialize" ? rec.props.$mcp_client_name : undefined);
      if (props.client_kind === "monitor") props.$process_person_profile = false;
      phCapture(env, ctx, request, [{ event: rec.event, properties: props, distinctId: visitor }]);
    }
    return jsonResponse(status, resp);
  },

  // Daily (wrangler.jsonc triggers.crons): the Hugging Face dataset's
  // download and like counts, for PostHog.
  async scheduled(_controller: ScheduledController, env: Env, ctx: ExecutionContext): Promise<void> {
    if (!env.POSTHOG_KEY) return;
    try {
      phCapture(env, ctx, undefined, await hfStatsEvents());
    } catch (e) {
      console.error("hf stats:", e);
    }
  },
};
