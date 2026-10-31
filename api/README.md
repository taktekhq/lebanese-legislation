# API

The REST API, published as `ghcr.io/taktekhq/lebanese-legislation-api`. It is the same Go code as the website and the MCP server ([`internal/`](../internal/)), serving only `/api/v1/` and `/healthz`. LebaneseLegislation.com serves the same routes.

| Route | Returns |
| --- | --- |
| `GET /api/v1/` | The list of routes |
| `GET /api/v1/search/?q=` | Laws, articles, rulings and Gazette issues by keyword or citation. `type=laws\|articles\|rulings\|gazette`, `year`, `limit` (1 to 50), `offset`. `semantic=true` adds `related` passages found by meaning |
| `GET /api/v1/laws/` | Laws, newest first |
| `GET /api/v1/law/{id}/` | A law: metadata, outline and amendments |
| `GET /api/v1/law/{id}/article/{number}/` | An article: original text, versions, changes and citing rulings. Civil Procedure articles carry `calculated_current` when an amount was updated by decree 330819 |
| `GET /api/v1/ruling/{id}/` | A court ruling, with its scanned pages' readings |
| `GET /api/v1/gazette/` and `/api/v1/gazette/{id}/` | Official Gazette issues |

Exact and citation search need no key. Search by meaning needs your own `GEMINI_API_KEY`: Gemini rewrites the question and embeds it, and PostgreSQL compares it with the saved passage vectors. Every returned passage is copied from the source text and links to it.

Run it from the repository root against a restored database:

```sh
DATABASE_URL='host=localhost port=5433 dbname=lebleg user=lebleg_reader password=...' go run ./api
```

It listens on `127.0.0.1:8000` (`LISTEN_ADDR` changes it). Build the image with `docker build -f api/Dockerfile .`. Settings are in the [service reference](../docs/reference.md#configuration).
