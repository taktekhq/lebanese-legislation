# What AI agents find here

An agent that lands on LebaneseLegislation.com can find the API, the MCP server and the rules for using the texts without being told where they are. Everything below is public and needs no key and no sign-in.

| What | Where | Served by |
| --- | --- | --- |
| Link headers on every page (`api-catalog`, `service-desc`, `service-doc`, `describedby`) | any HTML page | Worker for static pages, Go server for the rest |
| A Markdown version of every page | the page's own address, with `Accept: text/markdown` | Worker ([`markdown.js`](../workers/static-site/src/markdown.js)) |
| Content Signals: `search=yes, ai-input=yes, ai-train=yes` | `/robots.txt`, in every group | Go server |
| API catalog (RFC 9727) | `/.well-known/api-catalog` | Go server |
| MCP server card | `/.well-known/mcp/server-card.json` | Go server |
| A2A agent card, and the lookup it describes | `/.well-known/agent-card.json`, `POST /a2a` | Go server |
| Agent skill: how to find, read and cite a text | `/.well-known/agent-skills/index.json` | Go server |
| All of them in one list (ARD) | `/.well-known/ai-catalog.json` | Go server |
| Who runs the site (Personal Agent Protocol) | `/.well-known/poppy.json` | Go server |
| Search and article lookup as browser tools (WebMCP) | `/webmcp.js`, loaded by every page | static file |
| DNS records for agents (DNS-AID) | `_mcp._agents` and `_a2a._agents` SVCB records, DNSSEC signed | Cloudflare DNS |

The Go side is [`internal/site/agents.go`](../internal/site/agents.go) and [`a2a.go`](../internal/site/a2a.go). The files are built once at start and name only addresses that answer; `TestAgentFilesNameOnlyWhatExists` fails when one does not.

## Markdown

```sh
curl -H 'Accept: text/markdown' https://lebaneselegislation.com/en/law/190374/article/50/
```

The Worker converts the HTML page it already serves, so nothing is stored twice. The reply starts with a short front matter (title, description, address, language), then the content of `<main>`, then the footer's "not legal advice" notice. The header, menu, footer, forms and buttons are left out. No character of a legal text is escaped or changed. `X-Markdown-Tokens` is an estimate, four characters a token.

Markdown is served when the `Accept` header names `text/markdown` with a weight higher than `text/html`, or the same weight and named first (`text/markdown, text/html`). A browser never does, so it always gets HTML.

Pages the origin renders (a search, a filtered list, any address with a query) are converted the same way, after the origin has answered, and are not kept at the edge. isitagentready.com asks for the home page with a query of its own, so its Markdown check reads one of these. The self-hosted image has no Worker in front and answers HTML only.

## A2A

`POST /a2a` takes one JSON-RPC message (`message/send` for A2A 0.3, `SendMessage` for 1.0) and replies with one message: the texts that match, as plain text and as the same JSON `/api/v1/search/` returns. It is a search with no language model behind it. It has the per-address limit and the database queue of `/api/v1/`: a 429 or a 503 carries `Retry-After`.

```sh
curl -X POST https://lebaneselegislation.com/a2a -H 'Content-Type: application/json' -d '{
  "jsonrpc": "2.0", "id": 1, "method": "message/send",
  "params": {"message": {"role": "user", "parts": [{"kind": "text", "text": "قانون العمل"}]}}}'
```

## No OAuth metadata, on purpose

Nothing here needs a sign-in, so `/.well-known/oauth-authorization-server`, `/.well-known/oauth-protected-resource` and `/.well-known/openid-configuration` answer a short 404, and there is no `/auth.md`. MCP clients ask for those addresses before they connect (one client asked 19,071 times in a day in October 2026). Metadata there would send them into a sign-in that does not exist and break the connection for people who use the MCP server today. `TestProbesAnsweredWithoutAPage` and `TestAgentFilesPublishNoOAuth` hold this.

## Check it

[isitagentready.com](https://isitagentready.com/) scans the site. The site sells nothing and has no sign-in, so scan it with the commerce and sign-in checks switched off:

```sh
curl -X POST https://isitagentready.com/api/scan -H 'Content-Type: application/json' -d '{
  "url": "https://lebaneselegislation.com",
  "enabledChecks": ["robotsTxt", "sitemap", "linkHeaders", "dnsAid", "markdownNegotiation",
    "robotsTxtAiRules", "contentSignals", "webBotAuth", "apiCatalog", "mcpServerCard",
    "a2aAgentCard", "agentSkills", "webMcp", "ard", "personalAgentProtocol"]}'
```

Left out: `oauthDiscovery`, `oauthProtectedResource` and `authMd` (sign-in), and `x402`, `mpp`, `ucp`, `acp` and `ap2` (commerce). A check that is left out comes back `neutral`. On the website, the same choice is under "Customize scan". Every check in the list passes; `webBotAuth` is neutral, since the site sends no bots of its own. A scan with everything on fails the three sign-in checks, by design (see above).

Space scans 25 seconds apart: the scanner limits how often it will open a page in its browser.

## Deploy

The Go files ship with the origin (`origin.yml`). The Link header and Markdown for static pages are in the site Worker, which deploys only when `origin.yml` is run with `deploy_site_worker` on. A change to `layout.html` (the `webmcp.js` tag) is a new frame for each language, not a new copy of every page.
