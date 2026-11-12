# Connect to the public MCP server

Search and read Lebanese laws, articles, court rulings and Gazette issues. Free, with no account or API key.

Endpoint: `https://lebaneselegislation.com/mcp` (streamable HTTP).
Registry name: `com.lebaneselegislation/mcp`.
[Setup guide](https://lebaneselegislation.com/en/ai/).

The registry descriptor is [`server.json`](../server.json). The client configuration is [`.mcp.json`](../.mcp.json). Both use the existing public endpoint. Local Docker setup is in the [root README](../README.md).

## Connect

Claude Code:

```sh
claude mcp add --transport http lebaneselegislation https://lebaneselegislation.com/mcp
```

Cursor (`mcp.json`):

```json
{
  "mcpServers": {
    "lebaneselegislation": {
      "url": "https://lebaneselegislation.com/mcp"
    }
  }
}
```

VS Code (`.vscode/mcp.json`):

```json
{
  "servers": {
    "lebaneselegislation": {
      "type": "http",
      "url": "https://lebaneselegislation.com/mcp"
    }
  }
}
```

Clients that only run local servers can bridge with `mcp-remote`:

```json
{
  "mcpServers": {
    "lebaneselegislation": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "https://lebaneselegislation.com/mcp"]
    }
  }
}
```

## Tools

| Tool | What it returns |
|---|---|
| `search_legislation` | Laws, articles, rulings or Gazette issues matching Arabic, French or English words, with verbatim snippets |
| `get_law` | A law's metadata, table of contents, articles and the texts it amended or was amended by |
| `get_article` | One article's text in force today, its status and amendment history |
| `get_law_texts` | All of a law's article texts, a page at a time |
| `get_ruling` | One court ruling, its text or scan reading, and the link to the original PDF |
| `get_gazette_issue` | One Official Gazette issue and the texts it published |
| `get_links` | Citation and amendment links into or out of a law, article, ruling or issue |
| `list_legislation` | Laws, decrees and decisions by publication date |
| `list_gazette_issues` | Gazette issues by publication date |
| `possibly_affected` | General repeal clauses that may touch a law, as a list to check |

## Paging

Since 2.0.0 (6 Nov 2026), `get_law_texts`, `get_links`, `list_legislation` and `list_gazette_issues` page with a cursor: pass the reply's `next_cursor` back as `cursor`. A call with `offset` gets a tool error that says so. Every tool is marked read-only (`readOnlyHint`), so clients that confirm writes do not ask before each call. See [mcp/README.md](../mcp/README.md).

## Speed

Replies are prepared when the server's image is built, not computed per call. Real `get_article` calls on the hosted server had a median of 13 ms and a p95 of 131 ms of server time in the 12 hours after the 9 Nov 2026 change (128 and 1,060 ms in the week before). See [how the build makes reads fast](performance.md).

## Limits

60 requests a minute per client IP, and at most 30 requests in any 10 seconds per IP at Cloudflare's edge (on `/mcp`, `/a2a`, `/api/` and `/search/`). Past either, a 429. Tool calls also share the database: when the server is busy for more than 2 seconds a call gets a 503 with `Retry-After: 2`.

## Code and datasets

The former private `taktekhq/lebaneselegislation` code, scraper, build pipeline,
website and MCP are now in [this public repository](https://github.com/taktekhq/lebanese-legislation).
See [architecture](architecture.md) for the website's Cloudflare
Worker, R2 and container setup. Hosted semantic search uses Vectorize index
`lebleg-chunks`; the vectors are openly available as
[`vectors/vectors.parquet`](https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet). See the
[DuckDB example](reference.md#vectors).
