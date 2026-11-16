import { SECURITY_HEADERS } from "./headers.js";

// mcp.lebaneselegislation.com is a second address for the MCP server, in the
// form other MCP providers use (mcp.<domain>/mcp). The Worker turns its
// requests into requests for lebaneselegislation.com, so the origin, the
// rate limit and the telemetry see one MCP server whichever address a client
// was given. Only the MCP endpoint and the discovery files under /.well-known/
// are answered here; every other address moves to the main site, so the site
// never shows under two names.
export const MCP_HOST = "mcp.lebaneselegislation.com";
export const SITE = "https://lebaneselegislation.com";

// mcpHost returns null for any other host, { request } for a request to pass
// on as the same request for lebaneselegislation.com, or { response } for a
// redirect to the main site.
export function mcpHost(request) {
  const url = new URL(request.url);
  if (url.hostname !== MCP_HOST) return null;
  let path = url.pathname;
  // A client given the bare host posts to "/": that is the MCP endpoint too.
  if (path === "/" && request.method !== "GET" && request.method !== "HEAD") path = "/mcp";
  if (path === "/mcp" || path === "/mcp/" || path.startsWith("/.well-known/")) {
    return { request: new Request(SITE + path + url.search, request) };
  }
  const to = path === "/" ? SITE + "/en/ai/" : SITE + path + url.search;
  return { response: new Response(null, { status: 301, headers: { ...SECURITY_HEADERS, Location: to, "Cache-Control": "public, max-age=3600" } }) };
}
