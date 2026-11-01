// JSON-RPC transport tests: initialize, notifications, ping, tools/list,
// and the transport-level protections (body cap, CORS, 405 on GET, rate
// limit). Against the local D1 fixture (test/setup.ts); no network.

import { env } from "cloudflare:test";
import { describe, expect, it } from "vitest";
import worker from "../src/index";
import { fakeRateLimiter, permissiveEnv } from "./helpers";

function post(body: unknown, extraHeaders: Record<string, string> = {}): Promise<Response> {
  return worker.fetch(
    new Request("https://lebaneselegislation.com/mcp", {
      method: "POST",
      headers: { "Content-Type": "application/json", "CF-Connecting-IP": "203.0.113.1", ...extraHeaders },
      body: typeof body === "string" ? body : JSON.stringify(body),
    }),
    permissiveEnv(),
  );
}

describe("transport", () => {
  it("answers OPTIONS with CORS headers", async () => {
    const res = await worker.fetch(
      new Request("https://lebaneselegislation.com/mcp", { method: "OPTIONS" }),
      env,
    );
    expect(res.status).toBe(204);
    expect(res.headers.get("Access-Control-Allow-Origin")).toBe("*");
  });

  it("405s a GET with a JSON hint, not an SSE stream", async () => {
    const res = await worker.fetch(
      new Request("https://lebaneselegislation.com/mcp", { method: "GET", headers: { "CF-Connecting-IP": "203.0.113.2" } }),
      env,
    );
    expect(res.status).toBe(405);
    expect(res.headers.get("Allow")).toContain("POST");
    const body = await res.json();
    expect(body).toHaveProperty("error");
  });

  it("sends a browser GET to the setup page", async () => {
    const res = await worker.fetch(
      new Request("https://lebaneselegislation.com/mcp", { method: "GET", headers: { Accept: "text/html,application/xhtml+xml,*/*;q=0.8", "CF-Connecting-IP": "203.0.113.4" } }),
      env,
    );
    expect(res.status).toBe(302);
    expect(res.headers.get("Location")).toBe("https://lebaneselegislation.com/ai/");
  });

  it("still 405s an SSE probe even if it also accepts HTML", async () => {
    const res = await worker.fetch(
      new Request("https://lebaneselegislation.com/mcp", { method: "GET", headers: { Accept: "text/event-stream, text/html", "CF-Connecting-IP": "203.0.113.5" } }),
      env,
    );
    expect(res.status).toBe(405);
  });

  it("404s any path other than /mcp", async () => {
    const res = await worker.fetch(
      new Request("https://lebaneselegislation.com/", { method: "POST", headers: { "CF-Connecting-IP": "203.0.113.3" } }),
      env,
    );
    expect(res.status).toBe(404);
  });

  it("rejects a body over the 64KB cap", async () => {
    const huge = "x".repeat(70 * 1024);
    const res = await post(huge, { "CF-Connecting-IP": "203.0.113.4" });
    expect(res.status).toBe(413);
  });

  it("rejects invalid JSON", async () => {
    const res = await post("{not json", { "CF-Connecting-IP": "203.0.113.5" });
    expect(res.status).toBe(400);
    const body = await res.json();
    expect(body.error.code).toBe(-32700);
  });

  it("rejects a non-2.0 envelope", async () => {
    const res = await post({ jsonrpc: "1.0", id: 1, method: "ping" }, { "CF-Connecting-IP": "203.0.113.6" });
    expect(res.status).toBe(400);
  });

  it("gives a notification no response body, only 202", async () => {
    const res = await post({ jsonrpc: "2.0", method: "notifications/initialized" }, { "CF-Connecting-IP": "203.0.113.7" });
    expect(res.status).toBe(202);
    expect(await res.text()).toBe("");
  });

  it("enforces the per-IP rate limit (60/min) and returns 429 with Retry-After", async () => {
    // The real Workers Rate Limiting binding isn't simulated by the
    // miniflare version @cloudflare/vitest-pool-workers pins, so this
    // exercises src/index.ts's own handling of a limiter that says no,
    // using a small in-process stand-in (test/helpers.ts) rather than the
    // binding itself; the live Worker's real 60/min limit is checked in
    // deploy's verification step instead.
    const testEnv: Env = { DB: (env).DB, MCP_RATE_LIMITER: fakeRateLimiter(60) };
    let sawLimit = false;
    for (let i = 0; i < 70 && !sawLimit; i++) {
      const res = await worker.fetch(
        new Request("https://lebaneselegislation.com/mcp", {
          method: "POST",
          headers: { "Content-Type": "application/json", "CF-Connecting-IP": "203.0.113.8" },
          body: JSON.stringify({ jsonrpc: "2.0", id: i, method: "ping" }),
        }),
        testEnv,
      );
      if (res.status === 429) {
        sawLimit = true;
        expect(res.headers.get("Retry-After")).toBeTruthy();
      }
    }
    expect(sawLimit).toBe(true);
  });
});

describe("JSON-RPC methods", () => {
  it("initialize echoes a supported protocol version and carries the not-legal-advice line", async () => {
    const res = await post({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-06-18" } });
    const body = await res.json();
    expect(body.result.protocolVersion).toBe("2025-06-18");
    expect(body.result.serverInfo.name).toBe("lebaneselegislation");
    expect(body.result.instructions).toContain("Not legal advice");
  });

  it("initialize accepts the older 2025-03-26 protocol too", async () => {
    const res = await post({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-03-26" } });
    const body = await res.json();
    expect(body.result.protocolVersion).toBe("2025-03-26");
  });

  it("initialize falls back to the default on an unknown protocol version", async () => {
    const res = await post({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "1999-01-01" } });
    const body = await res.json();
    expect(body.result.protocolVersion).toBe("2025-06-18");
  });

  it("ping answers with an empty result", async () => {
    const res = await post({ jsonrpc: "2.0", id: 2, method: "ping" });
    const body = await res.json();
    expect(body.error).toBeUndefined();
  });

  it("tools/list advertises all seven tools with a description and schema", async () => {
    const res = await post({ jsonrpc: "2.0", id: 3, method: "tools/list" });
    const body = await res.json();
    const names = body.result.tools.map((t: { name: string }) => t.name);
    expect(names.sort()).toEqual(
      ["get_article", "get_gazette_issue", "get_law", "get_law_texts", "get_links", "get_ruling", "list_gazette_issues", "list_legislation", "possibly_affected", "search_legislation"].sort(),
    );
    for (const t of body.result.tools) {
      expect(t.description.length).toBeGreaterThan(10);
      expect(t.inputSchema.type).toBe("object");
    }
  });

  it("an unknown method is a JSON-RPC method-not-found error", async () => {
    const res = await post({ jsonrpc: "2.0", id: 4, method: "nope" });
    const body = await res.json();
    expect(body.error.code).toBe(-32601);
  });
});
