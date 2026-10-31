# MCP server

The MCP server, published as `ghcr.io/taktekhq/lebanese-legislation-mcp`. It is the same Go code as the website and the API ([`internal/`](../internal/)), serving only `/mcp` (streamable HTTP) and `/healthz`. The free hosted server at `https://lebaneselegislation.com/mcp` runs the same tools: [connect a client](../docs/mcp.md).

| Tool | Does |
| --- | --- |
| `search_legislation` | Keyword and citation search across laws, articles, rulings and Gazette issues. `semantic: true` adds passages related by meaning |
| `get_law` | A law's metadata, outline and amendments |
| `get_article` | An article's original text, versions, changes and citing rulings |
| `get_ruling` | A court ruling and its page readings |
| `get_gazette_issue` | An Official Gazette issue |

Results are copied from the source texts, never written by a model. Search by meaning needs your own `GEMINI_API_KEY`.

Run it from the repository root against a restored database:

```sh
DATABASE_URL='host=localhost port=5433 dbname=lebleg user=lebleg_reader password=...' go run ./mcp
```

It listens on `127.0.0.1:8080` (`LISTEN_ADDR` changes it). In Compose it is on http://localhost:8001/mcp.

- [`cloudflare/`](cloudflare/): run this image on Cloudflare Containers.
- [`server.json`](../server.json) at the repository root is the [MCP Registry](https://registry.modelcontextprotocol.io/) entry for the hosted server, and [`.mcp.json`](../.mcp.json) the client configuration.
