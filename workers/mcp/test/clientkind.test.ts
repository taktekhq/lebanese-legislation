// Which MCP callers are people's assistants, which are monitoring and registry
// bots (they connect and list tools, never ask anything), and which are bare
// HTTP libraries. Real User-Agents from lebaneselegislation.com/mcp, 14-20 Oct 2026.

import { describe, expect, it } from "vitest";
import { clientKind } from "../src/clientkind";

describe("clientKind", () => {
  it("marks assistants", () => {
    for (const ua of ["Claude-User", "ChatGPT-User/1.0", "Perplexity-User/1.0", "MistralAI-User/1.0"]) {
      expect(clientKind(ua, undefined)).toBe("assistant");
    }
    expect(clientKind("node", "claude-ai")).toBe("assistant");
    expect(clientKind("node", "cursor-vscode")).toBe("assistant");
  });

  it("marks monitoring and registry bots", () => {
    for (const ua of [
      "SentinelOracle/0.1 (+https://glimind.com/opt-out; liveness-only, never invokes tools)",
      "mcpbeat/0.1 (+https://mcpbeat.com/bot/; liveness check)",
      "AgentRankProbe/0.1 (+https://spark.tail6ec125.ts.net/developers)",
      "rokmcp-collector/0.2 (+https://rokmcp.com/bot)",
      "agent-collector/0.1 (+health check; no payment is made)",
      "EveryMCP-Probe/1.0 (+https://everymcp.ai/probe)",
      "FraywickBot/1.0 (+https://fraywick.xyz/docs#bot)",
      "MCPWatch/0.1.0 (+mcpwatch@iyre.com) longitudinal MCP security research",
      "reliability-bureau-spike/0.1",
      "LEGION-tlog/1.0 (+https://legion-api.com/tlog/probe/)",
      "TaifoonHarvester/1.0.0 (+https://www.taifoon.io/docs/harvester)",
      "mcp-rugpull-research/1.0",
      "histor/0.1.0 (+https://histor.modelmarket.dev/#crawler; read-only: initialize + tools/list",
      "atpp-scan/1.0 (+https://trust.redthreadsec.com/technical)",
    ]) {
      expect(clientKind(ua, undefined), ua).toBe("monitor");
    }
  });

  it("marks bare HTTP libraries", () => {
    for (const ua of ["node", "undici", "Python/3.11 aiohttp/3.14.5", "python-httpx/0.28.1", "Go-http-client/2.0", "curl/8.7.1", ""]) {
      expect(clientKind(ua, undefined), ua).toBe("library");
    }
  });

  it("leaves anything else as other", () => {
    expect(clientKind("Enerlio GmbH FACTANKER marc@enerlio.de", undefined)).toBe("other");
  });
});
