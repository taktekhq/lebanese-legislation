# Website

The Go website behind [LebaneseLegislation.com](https://lebaneselegislation.com/), with Arabic and English pages and search. It is published as `ghcr.io/taktekhq/lebanese-legislation-all:latest`, and `./setup` at the repository root and `docker compose --profile all up -d all` run it on port 8080 with the public database baked in. The code is shared with the [API](../api/) and the [MCP server](../mcp/) in [`internal/`](../internal/).

Cloudflare serves about 760,000 static pages rendered from `data/` by
[`static-site.yml`](../.github/workflows/static-site.yml) and `cmd/static-export`
in 24 parallel shards, rendering only the pages whose data changed. The JavaScript Worker in `workers/static-site` serves the
pages and ruling PDFs from R2 bucket `lebleg-site`. Search pages, `/api/v1/`,
filters and Gazette scans go to one Cloudflare Container with PostgreSQL 18.6,
a baked-in database and this Go website server. The
[origin workflow](../.github/workflows/origin.yml) deploys it.

Hosted semantic vectors are in Cloudflare Vectorize index `lebleg-chunks`.
The [vectors workflow](../.github/workflows/vectors.yml) publishes them openly
as [`vectors/vectors.parquet`](https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet)
on Hugging Face. See [architecture](../docs/architecture.md) and the
[DuckDB example](../docs/reference.md#vectors). The pgvector settings below
remain available for local services.

Run it from the repository root with Go 1.27.1 or newer:

```sh
export DATABASE_URL='postgres://USER:PASSWORD@HOST/DATABASE?sslmode=require'
go run ./web
```

Open http://localhost:8080. `LISTEN_ADDR` defaults to `127.0.0.1:8080`. `PUBLIC_URL` sets absolute links; without it they point to LebaneseLegislation.com (the image sets `http://localhost:8080`). `GEMINI_API_KEY` turns on search by meaning.

## Flags and settings

Every flag is optional. The same flags work for `api/` and `mcp/`, which serve only their own routes.

| Flag | Meaning |
| --- | --- |
| `-db` | PostgreSQL connection string. Default `DATABASE_URL`, then `LEBLEG_DSN` |
| `-addr` | Address to listen on. Default `LISTEN_ADDR`, then `127.0.0.1:8080` (`8000` for the API) |
| `-events DIR` | Keep the anonymous usage record (search words and result counts, reads, AI sign-ups) in `DIR`, 90 days. Turns on the sign-up form on `/ai/`. `go run ./cmd/usage-stats -events DIR` adds up a day |
| `-scans DIR` | Page images of texts read from Gazette scans |
| `-official DIR` | Page images and PDFs of texts published from official scans |
| `-ruling-files DIR` | Judgment PDFs, served at `/ruling-files/<id>.pdf` |
| `-noindex` | Keep every page out of search engines |
| `-auth FILE` | Preview mode: `FILE` holds `user:password`, and every page asks for it |

A leading `serve` is accepted, so `web serve -addr ... -db ...` works too.

| Setting | Meaning |
| --- | --- |
| `POSTHOG_KEY`, `POSTHOG_HOST` | Server errors to PostHog error tracking. Off when unset |
| `ANALYTICS` | `off` stops pages loading the browser analytics (`static/ga.js`, `static/ph.js`), which hold LebaneseLegislation.com's own Google Analytics and PostHog IDs. The binary loads them; the images set `off`. Change the IDs before you turn them on for another site |
| `RATE_LIMIT_PER_MINUTE`, `RATE_LIMIT_BURST` | Per-client limit on `/api/v1/` and `/mcp`. Default 60 and 20; the images set 0, no limit |
| `LEBLEG_SEARCH_PROVIDER` | `openai`, `gemini` or `claude`: search by meaning with that provider and its key (`OPENAI_API_KEY`, `GEMINI_API_KEY`, `ANTHROPIC_API_KEY`). `LEBLEG_SEARCH_MODEL`, `LEBLEG_SEARCH_SELECT_MODEL`, `LEBLEG_SEARCH_DAILY_LIMIT` (default 500) and `LEBLEG_SEARCH_SHARES_DATA` tune it |
| `LEBLEG_VECTOR_DSN` | A separate pgvector database for the passage vectors (see [`cmd/searchindex`](../cmd/searchindex/)), embedded with `GEMINI_API_KEY` and `LEBLEG_EMBEDDING_MODEL` |
| `GEMINI_API_KEY` alone | Without the two above: Gemini search by meaning, with the vectors in the main database. The other `GEMINI_*` settings are in the [service reference](../docs/reference.md#configuration) |
| `MCP_ALLOWED_ORIGINS` | Browser origins `/mcp` answers, beyond its own localhost address |

## Related commands

| Command | What it does |
| --- | --- |
| [`cmd/usage-stats`](../cmd/usage-stats/) | Adds up one day of the `-events` record, as JSON |
| [`cmd/journal-sitemap`](../cmd/journal-sitemap/) | Prints the project journal's sitemap; no database |
| [`cmd/searchindex`](../cmd/searchindex/) | Builds the semantic index in its own pgvector database |
| [`cmd/topicgen`](../cmd/topicgen/) | Writes and checks the topic pages (`internal/site/topics`) with a model that sees only the cited articles |
| [`cmd/check-english`](../cmd/check-english/) | Checks that English stays an unofficial aid |

The database needs the tables in [`schema/`](schema/). A restore of the public dump adds what the dump lacks ([`web.sql`](../tools/src/lebleg/sql/web.sql)). Four tables stay empty there because their data is not public: Constitutional Council rulings, noindex marks, page change dates and official Gazette texts.

Build the image from the repository root:

```sh
docker build -f workers/static-site/container/Dockerfile -t ghcr.io/taktekhq/lebanese-legislation-all:latest .
```

## Try it on fictional data

```sh
docker compose -f web/compose.demo.yaml up -d --wait
export DATABASE_URL='postgres://postgres@localhost:55439/legislation?sslmode=disable'
go run ./web
```

Search for `المكتبة`, open `/law/1/`, or query `/api/v1/search/?q=المكتبة`. Stop with `docker compose -f web/compose.demo.yaml down -v`.

```sh
go test ./...
DEMO_DATABASE_URL="$DATABASE_URL" go test ./internal/site -run TestSyntheticServing
```
