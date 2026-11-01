// Which PostHog events MCP requests, badge views and the daily cron send,
// in PostHog's $mcp_* shape so its MCP analytics screens work. Outbound
// fetches (PostHog, GA, the Hugging Face API) are stubbed; no network.

import { afterEach, describe, expect, it, vi } from "vitest";
import worker, { mcpErrorType, mcpSessionId } from "../src/index";
import { fakeRateLimiter, permissiveEnv } from "./helpers";

const BROWSER_UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36";
const HF_URL = "https://huggingface.co/datasets/taktekhq/lebanese-legislation";

function phEnv(): Env {
  return {
    ...permissiveEnv(),
    POSTHOG_KEY: "phc_test", POSTHOG_HOST: "https://ph.example",
    GA_MCP_MEASUREMENT_ID: "G-MCP", GA_MCP_API_SECRET: "s",
    GA_HF_MEASUREMENT_ID: "G-HF", GA_HF_API_SECRET: "s",
  };
}

function fakeCtx(): ExecutionContext & { settle: () => Promise<unknown> } {
  const pending: Promise<unknown>[] = [];
  return {
    waitUntil: (p) => { pending.push(p); },
    passThroughOnException: () => {},
    settle: () => Promise.all(pending),
  };
}

type Sent = { ph: Array<{ event: string; properties: Record<string, any> }>; ga: string[] };

// stubAll records PostHog batches and GA event names, and answers the
// Hugging Face API with `hf`.
function stubAll(hf: unknown = {}): Sent {
  const sent: Sent = { ph: [], ga: [] };
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = String(input);
    if (url.startsWith("https://ph.example/")) sent.ph.push(...JSON.parse(String(init?.body)).batch);
    else if (url.includes("google-analytics.com")) sent.ga.push(...JSON.parse(String(init?.body)).events.map((e: any) => e.name));
    else if (url.startsWith("https://huggingface.co/api/")) return Response.json(hf);
    return new Response(null, { status: 200 });
  });
  return sent;
}

async function rpc(body: unknown): Promise<Sent> {
  const sent = stubAll();
  const ctx = fakeCtx();
  await worker.fetch(
    new Request("https://lebaneselegislation.com/mcp", {
      method: "POST",
      headers: { "Content-Type": "application/json", "CF-Connecting-IP": "203.0.113.40", "User-Agent": "claude-user/1.0" },
      body: JSON.stringify(body),
    }),
    phEnv(),
    ctx,
  );
  await ctx.settle();
  vi.restoreAllMocks();
  return sent;
}

afterEach(() => vi.restoreAllMocks());

describe("PostHog MCP events", () => {
  it("initialize sends $mcp_initialize with the client", async () => {
    const { ph } = await rpc({
      jsonrpc: "2.0", id: 1, method: "initialize",
      params: { protocolVersion: "2025-06-18", clientInfo: { name: "claude-ai", version: "0.1.0" } },
    });
    expect(ph).toHaveLength(1);
    expect(ph[0].event).toBe("$mcp_initialize");
    expect(ph[0].properties).toMatchObject({
      $mcp_client_name: "claude-ai", $mcp_client_version: "0.1.0", $mcp_protocol_version: "2025-06-18",
      $mcp_server_name: "lebaneselegislation", $mcp_source: "lebaneselegislation_worker",
    });
  });

  it("tools/list sends mcp_tools_list with the advertised names", async () => {
    const { ph } = await rpc({ jsonrpc: "2.0", id: 2, method: "tools/list" });
    expect(ph[0].event).toBe("mcp_tools_list");
    expect(ph[0].properties.$mcp_listed_tool_names).toContain("search_legislation");
  });

  it("tools/call sends $mcp_tool_call with the SDK properties, and GA still gets its event", async () => {
    const { ph, ga } = await rpc({
      jsonrpc: "2.0", id: 3, method: "tools/call",
      params: { name: "search_legislation", arguments: { query: "ph-test إيجار", kind: "laws" } },
    });
    expect(ph).toHaveLength(1);
    const p = ph[0].properties;
    expect(ph[0].event).toBe("$mcp_tool_call");
    expect(p).toMatchObject({
      $mcp_tool_name: "search_legislation", $mcp_is_error: false, $mcp_cache_hit: false,
      $mcp_parameters: { query: "ph-test إيجار", kind: "laws" },
      $mcp_server_name: "lebaneselegislation", $mcp_client_name: "claude-user/1.0",
    });
    expect(typeof p.$mcp_duration_ms).toBe("number");
    expect(typeof p.$mcp_result_count).toBe("number");
    expect(typeof p.$mcp_response).toBe("string");
    expect(p.$session_id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(ga).toContain("mcp_tool_call");
  });

  it("an unknown tool is a validation error", async () => {
    const { ph } = await rpc({ jsonrpc: "2.0", id: 4, method: "tools/call", params: { name: "nope", arguments: {} } });
    expect(ph[0].properties).toMatchObject({ $mcp_tool_name: "nope", $mcp_is_error: true, $mcp_error_type: "validation" });
  });

  it("a tool that rejects its arguments is a validation error", async () => {
    const { ph } = await rpc({
      jsonrpc: "2.0", id: 5, method: "tools/call",
      params: { name: "search_legislation", arguments: { query: "x", kind: "bogus" } },
    });
    expect(ph[0].properties).toMatchObject({ $mcp_is_error: true, $mcp_error_type: "validation" });
  });

  it("ping sends nothing", async () => {
    expect((await rpc({ jsonrpc: "2.0", id: 6, method: "ping" })).ph).toHaveLength(0);
  });
});

describe("mcpErrorType", () => {
  it("tells a timeout from any other failure", () => {
    expect(mcpErrorType(new Error("tool call timed out"))).toBe("timeout");
    expect(mcpErrorType(new Error("D1_ERROR: boom"))).toBe("internal");
  });
});

describe("mcpSessionId", () => {
  it("is a UUIDv7 stamped with the day, stable within it", () => {
    const a = mcpSessionId("ab".repeat(32), "2026-10-20");
    expect(a).toBe(mcpSessionId("ab".repeat(32), "2026-10-20"));
    expect(a).not.toBe(mcpSessionId("ab".repeat(32), "2026-10-21"));
    expect(parseInt(a.replace(/-/g, "").slice(0, 12), 16)).toBe(Date.UTC(2026, 9, 20));
  });
});

describe("PostHog badge views", () => {
  async function badge(ua: string): Promise<Sent> {
    const sent = stubAll();
    const ctx = fakeCtx();
    await worker.fetch(
      new Request("https://lebaneselegislation.com/badge/legislation.svg?src=hf", { headers: { "User-Agent": ua, "CF-Connecting-IP": "203.0.113.41", Referer: HF_URL } }),
      phEnv(),
      ctx,
    );
    await ctx.settle();
    return sent;
  }

  it("a browser view of the dataset card sends a $pageview", async () => {
    const { ph } = await badge(BROWSER_UA);
    expect(ph).toHaveLength(1);
    expect(ph[0].event).toBe("$pageview");
    expect(ph[0].properties).toMatchObject({ $current_url: HF_URL, $referrer: HF_URL, badge: "legislation" });
  });

  it("a bot view sends nothing", async () => {
    expect((await badge("Googlebot/2.1")).ph).toHaveLength(0);
  });
});

describe("daily Hugging Face stats", () => {
  it("the cron sends hf_dataset_stats with the API's numbers", async () => {
    const sent = stubAll({ downloads: 12, downloadsAllTime: 40, likes: 3 });
    const ctx = fakeCtx();
    await worker.scheduled({ cron: "17 3 * * *", scheduledTime: Date.now() } as ScheduledController, phEnv(), ctx);
    await ctx.settle();
    expect(sent.ph).toHaveLength(1);
    expect(sent.ph[0].event).toBe("hf_dataset_stats");
    expect(sent.ph[0].properties).toMatchObject({ downloads_30d: 12, downloads_all_time: 40, likes: 3, dataset: "taktekhq/lebanese-legislation" });
  });
});

describe("client kind on PostHog MCP events", () => {
  async function rpcAs(ua: string): Promise<Sent> {
    const sent = stubAll();
    const ctx = fakeCtx();
    await worker.fetch(
      new Request("https://lebaneselegislation.com/mcp", {
        method: "POST",
        headers: { "Content-Type": "application/json", "CF-Connecting-IP": "203.0.113.50", "User-Agent": ua },
        body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
      }),
      phEnv(),
      ctx,
    );
    await ctx.settle();
    vi.restoreAllMocks();
    return sent;
  }

  it("a monitor is tagged and makes no person", async () => {
    const { ph } = await rpcAs("mcpbeat/0.1 (+https://mcpbeat.com/bot/; liveness check)");
    expect(ph[0].properties).toMatchObject({ client_kind: "monitor", $process_person_profile: false });
  });

  it("an assistant is tagged as one", async () => {
    const { ph } = await rpcAs("Claude-User");
    expect(ph[0].properties.client_kind).toBe("assistant");
    expect(ph[0].properties.$process_person_profile).toBeUndefined();
  });
});

describe("refused requests", () => {
  // Three requests from one IP against a limit of one a minute: the first
  // passes, the next two are refused, and only the first refusal is logged.
  it("a 429 sends one mcp_rate_limited per IP per minute", async () => {
    const sent = stubAll();
    const ctx = fakeCtx();
    const env: Env = { ...phEnv(), MCP_RATE_LIMITER: fakeRateLimiter(1), MCP_REFUSAL_LOG: fakeRateLimiter(1) };
    const statuses: number[] = [];
    for (let i = 0; i < 3; i++) {
      const res = await worker.fetch(
        new Request("https://lebaneselegislation.com/mcp", {
          method: "POST",
          headers: { "Content-Type": "application/json", "CF-Connecting-IP": "203.0.113.41", "User-Agent": "monthly-check/1.0" },
          body: JSON.stringify({ jsonrpc: "2.0", id: i, method: "tools/list" }),
        }),
        env,
        ctx,
      );
      statuses.push(res.status);
    }
    await ctx.settle();
    expect(statuses).toEqual([200, 429, 429]);
    const refused = sent.ph.filter((e) => e.event === "mcp_rate_limited");
    expect(refused).toHaveLength(1);
    expect(refused[0].properties).toMatchObject({
      $mcp_server_name: "lebaneselegislation", $user_agent: "monthly-check/1.0", limit_per_minute: 60,
    });
  });
});
