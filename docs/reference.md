# Service reference

Use the [Parquet dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation) for portable source tables, or the
[`-db` and `-all` images](../README.md#images) for PostgreSQL with both databases
baked from `data/`. API and MCP images are available for separate Compose services.
To add semantic search locally, [load the published vectors](../vectors/README.md).
No model training is required.

The hosted site uses Cloudflare R2, a Worker and one Container, with semantic
vectors in Vectorize. See [architecture](architecture.md).

## Public downloads and related resources

Use [LebaneseLegislation.com](https://lebaneselegislation.com/) for the public library, the [Parquet dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation) for portable tables, and the [collection](https://huggingface.co/collections/taktekhq/lebanese-legislation) for related scans and the handwriting demo. The code is MIT licensed; source texts retain their separate dataset terms, rights and attribution.

## Start locally

Install Docker with Compose. Allow at least 4 GB of available memory and 30 GB of free disk space; 8 GB of memory is recommended. The first start copies the image's baked databases into an empty volume.

```sh
git clone https://github.com/taktekhq/lebanese-legislation.git
cd lebanese-legislation
./start
```

`./start` generates separate random owner and reader passwords in a private `.env`
file. On an empty volume the DB image copies its baked cluster and sets these
passwords. API and MCP wait for both serving databases to be ready. Existing
volumes and credentials are preserved. Select a database image by date with
`DB_IMAGE=ghcr.io/taktekhq/lebanese-legislation-db:YYYY-MM-DD`. A new image needs
a fresh volume to replace the dataset; back up data you need first.

Open the website at <http://localhost:8080>, the API at <http://localhost:8000/api/v1/>, connect an MCP client to <http://localhost:8001/mcp>, or search:

```sh
curl --get 'http://localhost:8000/api/v1/search/' --data-urlencode 'q=قانون العمل'
```

Exact text and citation searches work without an AI key. To turn on search by meaning, [load vectors](../vectors/README.md), set your Gemini key and configure `LEBLEG_VECTOR_DSN` for `lebleg_search` in the service environment, then run:

```sh
docker compose up -d --force-recreate api mcp web
```

Arabic, English and French queries can retrieve multilingual source passages. English discovery titles are unofficial aids, not authoritative translations. Availability of a language depends on the source material.

All published ports bind to your own computer. The API has no user authentication and is meant for local use. Before exposing it publicly, set `RATE_LIMIT_PER_MINUTE` (LebaneseLegislation.com uses 60) or limit at your reverse proxy, and add HTTPS and an appropriate cost limit.

Stop services with `docker compose down`. This preserves the database volume. Volume lifecycle belongs to the operator. There is no automatic refresh. Use a different Compose project and fresh volumes to select a new snapshot.


## What you get

| Artifact | Purpose |
| --- | --- |
| Existing `data/*.parquet` on Hugging Face | Portable source tables for analysis and other databases |
| `ghcr.io/taktekhq/lebanese-legislation-db:latest` | PostgreSQL 18.6, pgvector 0.8.2 and both databases baked from `data/` |
| `ghcr.io/taktekhq/lebanese-legislation-api:0.4.0` | The REST API (`/api/v1/`) |
| `ghcr.io/taktekhq/lebanese-legislation-mcp:0.4.0` | The MCP server (`/mcp`) |
| `ghcr.io/taktekhq/lebanese-legislation-all:latest` | Both databases and the website, API and MCP |

Older version tags stay published. See [changes](changes.md).

The local database and hosted Cloudflare container share the same baked database image.

The database image is built only from the public tables of [`data/`](../data/) at a pinned commit, the same tables as the Parquet dataset. It does not contain private website tables, user queries, analytics, credentials or website-only explanations. Exported views become snapshot tables. `source_documents` is a derived read model for this immutable snapshot; editing raw tables does not automatically update it or its vectors. Article retrieval follows the exported in-force view at the stated date; the raw tables retain the wider historical corpus.

When present, `GET /api/v1/law/{id}/article/{number}/` and MCP `get_article` include the additive `calculated_current` object for Civil Procedure (law 244565) amounts updated by decree 330819. It is returned only when the shown text is exactly the in-force source, its SHA-256 matches and its authority and multiplier are valid. Original text and versions keep their fields. Older snapshots without the derived table omit the object.

## How retrieval works

1. Search matches original text, titles and explicit citations in PostgreSQL first: `GET /api/v1/search/` and MCP `search_legislation`.
2. With `semantic=true` (API) or `semantic: true` (MCP), and a `GEMINI_API_KEY` on the server, Gemini embeds the question with `gemini-embedding-001` (768 dimensions) and the hosted service searches Cloudflare Vectorize index `lebleg-chunks`. Local pgvector services compare against `semantic_chunks` (`halfvec(768)`). Found passages come back as `related`. Without a key the result says `"semantic": "unavailable"`. Adding `select=true` lets Gemini keep only the passages that answer the question; it returns passage IDs, never wording.
3. Every passage is reloaded from the source text and hash-checked before it is returned, with a link to its source. The website's search page also uses Gemini to rewrite a question into Arabic search terms.

A missing explicit citation is not silently replaced with a nearby semantic match. An absent or failed provider leaves ordinary search available. Retrieval can miss relevant material or return irrelevant passages. Source links and exact quotations make checking possible; they do not establish legal correctness or current legal status. Scanned ruling pages keep their extraction provenance and confidence fields. OCR can contain errors; consult the linked originals.

The API key stays in the server container. It is never sent to the browser or stored in the public dataset. Search by meaning sends the question to Google, not database credentials. Do not send confidential queries without considering your provider's terms.

Precomputed vectors save the cost of embedding the whole corpus. Query embeddings still need a compatible Gemini model. Another embedding model needs a separately rebuilt document index, even if it also has 768 dimensions.

## Configuration

Copy the optional settings from `.env.example`. `./start` creates required passwords when `.env` does not exist; an existing manually created `.env` must contain both passwords.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DB_PORT` | `5433` | Loopback PostgreSQL port |
| `API_PORT` | `8000` | Loopback API port |
| `MCP_PORT` | `8001` | Loopback MCP port |
| `WEB_PORT` | `8080` | Loopback website port |
| `PUBLIC_URL` | `http://localhost:$WEB_PORT` | The website's absolute links; set it when you host the site publicly |
| `DB_MEMORY` | `3g` | Database container cap; index construction needs headroom |
| `GEMINI_API_KEY` | empty | Your key; turns on search by meaning |
| `GEMINI_MAX_CALLS_PER_DAY` | `500` | Provider calls per UTC day for each service; `0` turns search by meaning off |
| `GEMINI_CONCURRENCY` | `2` | Provider calls at once, 1 to 16 |
| `GEMINI_QUERY_CACHE_SIZE` | `256` | Cached answers per cache, up to 256; `0` turns caching off |
| `GEMINI_QUERY_CACHE_TTL` | `3600` | Seconds an answer stays cached, up to 3600; `0` turns caching off |
| `RATE_LIMIT_PER_MINUTE` | `0` | Requests a minute per client IP for the API and MCP; `0` is no limit. Set it when you host them publicly. The images set `0`; the binary alone defaults to 60 |
| `RATE_LIMIT_BURST` | `20` | Requests a client may send at once above that rate |
| `MCP_ALLOWED_ORIGINS` | empty | Comma-separated browser origins the MCP server answers, beyond its own localhost address. Clients that send no `Origin` are always answered |
| `GEMINI_SEARCH_MODEL` | `gemini-3.8-flash` | The model that rewrites questions on the website's search page |
| `GEMINI_EMBEDDING_MODEL` | `gemini-embedding-001` | Must match the saved vectors |

The daily cap, the cache and the concurrency limit live in each service process. Restarting resets the counter, and API, MCP and website each have their own. Use provider billing limits for a durable spending ceiling.

To pin the baked dataset, select a dated `DB_IMAGE` and use a fresh volume.

## Use your own PostgreSQL

Start with the [`-db` image](../db/README.md), or load the checked-in source tables
with [`data/load.sh`](../data/load.sh) into a fresh database. The
[Hugging Face Parquet tables](https://huggingface.co/datasets/taktekhq/lebanese-legislation) support analysis and imports into other databases.
See [loading vectors](../vectors/README.md) for optional semantic search.

## Build images and develop

Every image builds from the repository root:

```sh
docker build -f db/Dockerfile -t lebleg-db-local .
docker build -f api/Dockerfile -t lebleg-api-local .
docker build -f mcp/Dockerfile -t lebleg-mcp-local .
docker build -f workers/static-site/container/Dockerfile -t ghcr.io/taktekhq/lebanese-legislation-all:latest .
go vet ./... && go test ./...
cd tools && pip install '.[build,test]' && pytest -q && ruff check src tests
```

Go tests that need a database use the fictional fixture in [`web/`](../web/README.md). Python tests that need PostgreSQL create and remove uniquely named databases when `LEBLEG_TEST_ADMIN_DSN` is set. Container tests run when `LEBLEG_TEST_IMAGE` is set, and the Compose test when the four `LEBLEG_TEST_*_IMAGE` variables are set. They never use your existing database. See the [checks workflow](../.github/workflows/checks.yml) for a disposable setup. `docker build -f db/Dockerfile --target postgres` gives a plain PostgreSQL with pgvector for those tests.

The DB image pins the official `postgres:18.6` base by digest and verifies the pgvector source checksum. The image publication workflow builds the four images for `linux/amd64,linux/arm64`, checks each one on both platforms against a fictional database, and refuses to overwrite version tags.

Set `LEBLEG_TEST_DB_IMAGE` to a locally built DB target to run the synthetic first-boot, restart and failure integration checks. These use tiny fixture dumps and disposable volumes, never the real dataset.

## Licence and attribution

Code: [MIT](../LICENSE), copyright Taktek and Nizar Mahmoud.

The dataset has its own licence, `other: public-texts-credit-the-centre`. Credit the **Centre for Research and Studies in Legal Informatics, Lebanese University** for the legal texts. Dataset structure: Taktek, LLC. The code's MIT licence does not replace the dataset's terms. Preserve source provenance and the distinction between Arabic legal texts and unofficial discovery aids.

## Vectors

[`vectors/vectors.parquet`](https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet) contains 1,547,937 semantic search chunks,
embedded with `gemini-embedding-001` in 768 dimensions. Columns: `id`,
`document`, `model`, `hash`, `values` (`FLOAT[768]`). The file is published openly
in the `taktekhq/lebanese-legislation` Hugging Face dataset.

Read it directly with DuckDB:

```sql
SELECT id, document, model, hash, values
FROM read_parquet('https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet')
LIMIT 5;
```

The hosted search uses Cloudflare Vectorize index `lebleg-chunks` instead of
storing vectors in PostgreSQL. The [vectors workflow](https://github.com/taktekhq/lebanese-legislation/blob/main/.github/workflows/vectors.yml)
runs `build`, then `publish` and `vectorize`. `cmd/export-vectors` exports the
chunks; `cmd/vectorize-sync` uploads only changed vectors on later runs.
