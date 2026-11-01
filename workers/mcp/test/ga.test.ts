// GA4 Measurement Protocol tests: the payload's shape, the per-day visitor
// ids, the stat badges and the views they count, and which events MCP
// requests send (each to its own stream). The
// outbound fetch to Google is stubbed; no network.

import { afterEach, describe, expect, it, vi } from "vitest";
import worker from "../src/index";
import { clip, gaPayload, visitorIds } from "../src/ga";
import { day, isBot, renderBadge, textWidth, thousands } from "../src/badge";
import { permissiveEnv } from "./helpers";

const BROWSER_UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36";

function trackedEnv(): Env {
  return {
    ...permissiveEnv(),
    GA_MCP_MEASUREMENT_ID: "G-MCP", GA_MCP_API_SECRET: "mcp-secret",
    GA_HF_MEASUREMENT_ID: "G-HF", GA_HF_API_SECRET: "hf-secret",
  };
}

// fakeCtx collects waitUntil promises so a test can await the sends.
function fakeCtx(): ExecutionContext & { settle: () => Promise<unknown> } {
  const pending: Promise<unknown>[] = [];
  return {
    waitUntil: (p) => { pending.push(p); },
    passThroughOnException: () => {},
    settle: () => Promise.all(pending),
  };
}

// stubGA replaces fetch and returns the Measurement Protocol bodies sent.
function stubGA(): { urls: string[]; bodies: Array<{ client_id: string; events: Array<{ name: string; params: Record<string, unknown> }> }> } {
  const sent = { urls: [] as string[], bodies: [] as any[] };
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    sent.urls.push(String(input));
    sent.bodies.push(JSON.parse(String(init?.body)));
    return new Response(null, { status: 204 });
  });
  return sent;
}

afterEach(() => vi.restoreAllMocks());

describe("gaPayload", () => {
  it("adds session and engagement to every event and drops empty params", () => {
    const body = gaPayload("1.2", "99", { country: "LB", city: "Beirut", regionCode: "BA" }, [
      { name: "mcp_tool_call", params: { tool_name: "get_law", search_term: undefined, search_kind: "" } },
    ]);
    expect(body).toEqual({
      client_id: "1.2",
      events: [{ name: "mcp_tool_call", params: { session_id: "99", engagement_time_msec: 1, tool_name: "get_law" } }],
      user_location: { country_id: "LB", city: "Beirut", region_id: "LB-BA" },
    });
  });

  it("clips long values to GA4's 100 characters", () => {
    const body = gaPayload("1.2", "9", undefined, [{ name: "e", params: { search_term: "ق".repeat(300) } }]) as any;
    expect(body.events[0].params.search_term).toHaveLength(100);
    expect(body.user_location).toBeUndefined();
    expect(clip("abc", 2)).toBe("ab");
  });
});

describe("visitorIds", () => {
  it("is stable within a day and changes across days", async () => {
    const a = await visitorIds("203.0.113.9", BROWSER_UA, "2026-10-20");
    const b = await visitorIds("203.0.113.9", BROWSER_UA, "2026-10-20");
    const c = await visitorIds("203.0.113.9", BROWSER_UA, "2026-10-21");
    expect(a).toEqual(b);
    expect(a.clientId).not.toBe(c.clientId);
    expect(a.clientId).toMatch(/^\d+\.\d+$/);
    expect(a.sessionId).toMatch(/^\d+$/);
  });
});

describe("badges", () => {
  function get(path: string, headers: Record<string, string> = {}, env: Env = trackedEnv()) {
    const ctx = fakeCtx();
    const res = worker.fetch(new Request("https://lebaneselegislation.com" + path, { headers }), env, ctx);
    return { res, ctx };
  }

  it("shows a live count from D1 as an uncached SVG", async () => {
    const { res } = get("/badge/legislation.svg");
    const r = await res;
    expect(r.status).toBe(200);
    expect(r.headers.get("Content-Type")).toContain("image/svg+xml");
    expect(r.headers.get("Cache-Control")).toContain("no-store");
    const svg = await r.text();
    expect(svg).toMatch(/<svg[^>]*aria-label="legislation: [\d,]+ texts"/);
  });

  it("counts a view in the Hugging Face stream only with ?src=hf", async () => {
    const sent = stubGA();
    const a = get("/badge/mcp.svg?src=hf", { "User-Agent": BROWSER_UA, "CF-Connecting-IP": "203.0.113.20", Referer: "https://huggingface.co/" });
    expect((await a.res).status).toBe(200);
    await a.ctx.settle();
    const b = get("/badge/mcp.svg", { "User-Agent": BROWSER_UA });
    await b.res;
    await b.ctx.settle();
    expect(sent.bodies).toHaveLength(1);
    expect(sent.urls[0]).toContain("measurement_id=G-HF");
    const ev = sent.bodies[0].events[0];
    expect(ev.name).toBe("page_view");
    expect(ev.params).toMatchObject({
      page_location: "https://lebaneselegislation.com/huggingface",
      page_referrer: "https://huggingface.co/",
      badge: "mcp",
    });
  });

  it("serves bots without counting them", async () => {
    const sent = stubGA();
    const { res, ctx } = get("/badge/mcp.svg?src=hf", { "User-Agent": "Googlebot-Image/1.0" });
    expect((await res).status).toBe(200);
    await ctx.settle();
    expect(sent.bodies).toHaveLength(0);
    expect(isBot("")).toBe(true);
    expect(isBot(BROWSER_UA)).toBe(false);
  });

  it("shows n/a when Hugging Face can't be read", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("down", { status: 503 }));
    const svg = await (await get("/badge/updated.svg").res).text();
    expect(svg).toContain('aria-label="last updated: n/a"');
  });

  it("shows when the dataset last changed on Hugging Face", async () => {
    const urls: string[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      urls.push(String(input));
      return Response.json({ id: "taktekhq/lebanese-legislation", lastModified: "2026-10-20T14:31:53.000Z" });
    });
    const r = await get("/badge/updated.svg").res;
    expect(r.status).toBe(200);
    expect(await r.text()).toContain('aria-label="last updated: 20 Oct 2026"');
    expect(urls[0]).toContain("huggingface.co/api/datasets/taktekhq/lebanese-legislation?expand[]=lastModified");
    expect(day("2026-01-05T23:59:00Z")).toBe("5 Jan 2026");
  });

  it("ignores an unknown src and 404s an unknown badge", async () => {
    const sent = stubGA();
    const a = get("/badge/mcp.svg?src=constructor", { "User-Agent": BROWSER_UA });
    expect((await a.res).status).toBe(200);
    await a.ctx.settle();
    expect(sent.bodies).toHaveLength(0);
    expect((await get("/badge/toString.svg").res).status).toBe(404);
    expect((await get("/badge/nope.svg").res).status).toBe(404);
  });

  it("sends nothing when the stream's secret is not set", async () => {
    const sent = stubGA();
    const { res, ctx } = get("/badge/mcp.svg?src=hf", { "User-Agent": BROWSER_UA }, permissiveEnv());
    await res;
    await ctx.settle();
    expect(sent.bodies).toHaveLength(0);
  });

  it("renders text that fits and escapes XML", () => {
    expect(thousands(389592)).toBe("389,592");
    expect(thousands(12)).toBe("12");
    const svg = renderBadge("a<b", "x&y");
    expect(svg).toContain("a&lt;b");
    expect(svg).toContain("x&amp;y");
    expect(textWidth("103,121 texts")).toBeGreaterThan(60);
  });
});

describe("MCP events", () => {
  async function rpc(body: unknown): Promise<{ status: number; events: Array<{ name: string; params: Record<string, unknown> }> }> {
    const sent = stubGA();
    const ctx = fakeCtx();
    const res = await worker.fetch(
      new Request("https://lebaneselegislation.com/mcp", {
        method: "POST",
        headers: { "Content-Type": "application/json", "CF-Connecting-IP": "203.0.113.30", "User-Agent": "claude-user/1.0" },
        body: JSON.stringify(body),
      }),
      trackedEnv(),
      ctx,
    );
    await ctx.settle();
    const events = sent.bodies.flatMap((b) => b.events);
    for (const u of sent.urls) expect(u).toContain("measurement_id=G-MCP");
    vi.restoreAllMocks();
    return { status: res.status, events };
  }

  it("records the client on initialize", async () => {
    const { events } = await rpc({
      jsonrpc: "2.0", id: 1, method: "initialize",
      params: { protocolVersion: "2025-06-18", clientInfo: { name: "claude-ai", version: "0.1.0" } },
    });
    expect(events).toHaveLength(1);
    expect(events[0].name).toBe("mcp_initialize");
    expect(events[0].params).toMatchObject({
      client_name: "claude-ai", client_version: "0.1.0", protocol_version: "2025-06-18",
      page_location: "https://lebaneselegislation.com/mcp", client_ua: "claude-user/1.0",
    });
  });

  it("records a search with its term, then the same call as a cache hit", async () => {
    const call = { jsonrpc: "2.0", id: 2, method: "tools/call", params: { name: "search_legislation", arguments: { query: "ga-test إيجار", kind: "laws" } } };
    const first = await rpc(call);
    expect(first.events[0]).toMatchObject({
      name: "mcp_tool_call",
      params: { tool_name: "search_legislation", search_term: "ga-test إيجار", search_kind: "laws", cache_hit: 0, is_error: 0 },
    });
    const second = await rpc(call);
    expect(second.events[0].params).toMatchObject({ cache_hit: 1 });
  });

  it("records an unknown tool as an error", async () => {
    const { events } = await rpc({ jsonrpc: "2.0", id: 3, method: "tools/call", params: { name: "nope", arguments: {} } });
    expect(events[0].params).toMatchObject({ tool_name: "nope", is_error: 1 });
  });

  it("records nothing for ping or a notification", async () => {
    expect((await rpc({ jsonrpc: "2.0", id: 4, method: "ping" })).events).toHaveLength(0);
    expect((await rpc({ jsonrpc: "2.0", method: "notifications/initialized" })).events).toHaveLength(0);
  });
});
