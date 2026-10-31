# Lebanese Legislation MCP

Search and read Lebanese laws, articles, court rulings and Gazette issues. Verbatim texts only.

A free public service of [lebaneselegislation.com](https://lebaneselegislation.com). No account, no key.

- Endpoint: `https://lebaneselegislation.com/mcp` (streamable HTTP)
- Registry name: `com.lebaneselegislation/mcp` ([official MCP registry](https://registry.modelcontextprotocol.io/v0/servers?search=lebaneselegislation))
- Setup guide: https://lebaneselegislation.com/en/ai/

The texts come from the Lebanese University's Centre for Research and Studies in Legal Informatics, copied exactly. Nothing a tool returns is written by a model. Arabic is the official text; English titles are an unofficial aid. Not legal advice.

## Source, data and related services

The consolidated [Lebanese Legislation repository](https://github.com/taktekhq/lebanese-legislation) contains the current website, PostgreSQL, API, MCP and public-data tools. For self-hosted services and Docker images, use its [setup and image links](https://github.com/taktekhq/lebanese-legislation#run-the-services-locally).

The [PostgreSQL dataset release](https://github.com/taktekhq/lebanese-legislation/releases/tag/data-2026-10-30.2) provides metadata, checksums and a pinned Hugging Face dump download. The [Parquet dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation) and [dataset collection](https://huggingface.co/collections/taktekhq/lebanese-legislation) remain available separately. Code is MIT licensed; the legal texts and scans retain their own rights and attribution.

## Connect

Claude Code:

```sh
claude mcp add --transport http lebaneselegislation https://lebaneselegislation.com/mcp
```

Cursor, VS Code and other clients (`mcp.json`):

```json
{
  "mcpServers": {
    "lebaneselegislation": {
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

## Limits

60 requests a minute per client IP.

## License

MIT for this repository. Official Lebanese texts are not subject to copyright; credit the Lebanese University's Centre when you quote them.
