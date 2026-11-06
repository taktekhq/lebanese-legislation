# MCP server

The MCP server, published as `ghcr.io/taktekhq/lebanese-legislation-mcp`. It is the same Go code as the website and the API ([`internal/`](../internal/)), serving only `/mcp` (streamable HTTP) and `/healthz`. The free hosted server at `https://lebaneselegislation.com/mcp` runs the same tools: [connect a client](../docs/mcp.md).

| Tool | Does |
| --- | --- |
| `search_legislation` | Keyword and citation search across laws, articles, rulings and Gazette issues. `semantic: true` adds passages related by meaning; `select: true` keeps those that answer the query |
| `get_law` | A law's metadata, full outline and amendments |
| `get_article` | An article's original text, versions, changes and citing rulings |
| `get_law_texts` | A law's article texts in order, a page at a time |
| `get_ruling` | A court ruling and its page readings |
| `get_gazette_issue` | An Official Gazette issue |
| `get_links` | The citations and amendments into or out of one item, a page at a time |
| `list_legislation` | Laws, decrees and decisions by publication date, a page at a time |
| `list_gazette_issues` | Gazette issues by publication date, a page at a time |
| `possibly_affected` | The general repeals ("all provisions contrary to this law are repealed") that may touch a law |

Results are copied from the source texts, never written by a model. Search by meaning needs your own `GEMINI_API_KEY`.

## 2.0.0: cursor paging

Since 2.0.0 (6 Nov 2026) the four tools that answer a page at a time (`get_law_texts`, `get_links`, `list_legislation`, `list_gazette_issues`) page with a cursor. An answer with more after it carries `next_cursor`; pass it unchanged as `cursor`, with the same other arguments, for the next page. The last page has no `next_cursor`. `total` is still the whole count. `offset` and `next_offset` are gone, and a call that passes `offset` gets a tool error saying to use `cursor`.

Before (1.x):

```json
{"name": "list_legislation", "arguments": {"since": "2025", "limit": 50, "offset": 50}}
-> {"total": 812, "offset": 50, "next_offset": 100, "laws": [...]}
```

After (2.0.0):

```json
{"name": "list_legislation", "arguments": {"since": "2025", "limit": 50}}
-> {"total": 812, "next_cursor": "djF8MjAyNS0xMS0wNHwyMDAwMjAwODkx", "laws": [...]}
{"name": "list_legislation", "arguments": {"since": "2025", "limit": 50, "cursor": "djF8MjAyNS0xMS0wNHwyMDAwMjAwODkx"}}
```

Why: an offset made Postgres read and throw away every row before the page (a deep page of laws took 0.85 s, and 3 to 9 s under a crawl). A cursor is a condition on the sort key that an index answers in under a millisecond at any depth, and a walk neither repeats nor skips a text while new ones are added nightly. It is also how MCP itself pages. To jump to a period, use `since` and `until`; to read one article, use `get_article`.

Run it from the repository root against a restored database:

```sh
DATABASE_URL='host=localhost port=5433 dbname=lebleg user=lebleg_reader password=...' go run ./mcp
```

It listens on `127.0.0.1:8080` (`LISTEN_ADDR` changes it). In Compose it is on http://localhost:8001/mcp. Browser requests are answered only from `MCP_ALLOWED_ORIGINS` or the same localhost address.

- The hosted server at https://lebaneselegislation.com/mcp is this code inside the `-all` image on Cloudflare Containers: see [workers/static-site/container](../workers/static-site/container/README.md).
- [`server.json`](../server.json) at the repository root is the [MCP Registry](https://registry.modelcontextprotocol.io/) entry for the hosted server, and [`.mcp.json`](../.mcp.json) the client configuration.

## Code and datasets

The former private `taktekhq/lebaneselegislation` code, scraper, build pipeline,
website and MCP are now in [this public repository](https://github.com/taktekhq/lebanese-legislation).
See [architecture](../docs/architecture.md) for the website's Cloudflare
Worker, R2 and container setup. Hosted semantic search uses Vectorize index
`lebleg-chunks`; the vectors are openly available as
[`vectors/vectors.parquet`](https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet). See the
[DuckDB example](../docs/reference.md#vectors).
