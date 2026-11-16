import test from "node:test";
import assert from "node:assert/strict";
import { mcpHost } from "../src/mcphost.js";

const body = '{"jsonrpc":"2.0","id":1,"method":"initialize"}';

test("other hosts are left alone", () => {
  assert.equal(mcpHost(new Request("https://lebaneselegislation.com/mcp", { method: "POST", body })), null);
  assert.equal(mcpHost(new Request("https://www.lebaneselegislation.com/")), null);
});

test("the MCP endpoint becomes the same request for lebaneselegislation.com/mcp", async () => {
  const r = mcpHost(new Request("https://mcp.lebaneselegislation.com/mcp", {
    method: "POST", body, headers: { "content-type": "application/json", "mcp-session-id": "s1" },
  }));
  assert.equal(r.request.url, "https://lebaneselegislation.com/mcp");
  assert.equal(r.request.method, "POST");
  assert.equal(r.request.headers.get("mcp-session-id"), "s1");
  assert.equal(await r.request.text(), body);
});

test("a POST to the bare host is the MCP endpoint", () => {
  const r = mcpHost(new Request("https://mcp.lebaneselegislation.com/", { method: "POST", body }));
  assert.equal(r.request.url, "https://lebaneselegislation.com/mcp");
});

test("GET, DELETE and discovery files pass through", () => {
  assert.equal(mcpHost(new Request("https://mcp.lebaneselegislation.com/mcp", { method: "DELETE" })).request.url, "https://lebaneselegislation.com/mcp");
  assert.equal(mcpHost(new Request("https://mcp.lebaneselegislation.com/mcp/")).request.url, "https://lebaneselegislation.com/mcp/");
  assert.equal(mcpHost(new Request("https://mcp.lebaneselegislation.com/.well-known/oauth-protected-resource")).request.url,
    "https://lebaneselegislation.com/.well-known/oauth-protected-resource");
});

test("pages move to the main site", () => {
  const home = mcpHost(new Request("https://mcp.lebaneselegislation.com/"));
  assert.equal(home.response.status, 301);
  assert.equal(home.response.headers.get("Location"), "https://lebaneselegislation.com/en/ai/");
  const law = mcpHost(new Request("https://mcp.lebaneselegislation.com/law/1/?x=1"));
  assert.equal(law.response.headers.get("Location"), "https://lebaneselegislation.com/law/1/?x=1");
});
