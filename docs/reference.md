# Service reference

Release `2026-10-30.2` contains 1,094,080 source records and 1,547,568 passages with precomputed embeddings. The 6,399,925,385-byte dump is tied to its source commit and checksum.

Restore the public [Lebanese Legislation dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation) into PostgreSQL with a precomputed pgvector index. Run an API that returns exact matches first and related source passages underneath.

The existing Parquet dataset remains the portable data distribution. This project adds a PostgreSQL snapshot and reusable search service. No model training is required.

Four images target Linux AMD64 and ARM64: `lebanese-legislation-db`, `lebanese-legislation-api`, `lebanese-legislation-mcp` and `lebanese-legislation-web`. The website, API and MCP are one Go codebase ([`internal/`](../internal/)) built as three binaries ([`web/`](../web/), [`api/`](../api/), [`mcp/`](../mcp/)). The Python [`tools/`](../tools/) restore, rebuild and publish the database.

## Public downloads and related resources

The PostgreSQL dump lives only on Hugging Face, in the dataset's [`releases/` folder](https://huggingface.co/datasets/taktekhq/lebanese-legislation/tree/main/releases). Each release is `releases/<release-id>/corpus.dump` with its `manifest.json`, which records the SHA-256, counts and versions. `releases/latest.json` points to the newest verified release. Dataset release IDs such as `2026-10-30.2` and software versions such as `0.2.0` are separate.

Use [LebaneseLegislation.com](https://lebaneselegislation.com/) for the public library, the [Parquet dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation) for portable tables, and the [collection](https://huggingface.co/collections/taktekhq/lebanese-legislation) for related scans and the handwriting demo. The code is MIT licensed; source texts retain their separate dataset terms, rights and attribution.

## Start locally

Install Docker with Compose. Allow at least 4 GB of available memory and 30 GB of free disk space; 8 GB of memory is recommended. The first start downloads the full release and rebuilds its indexes, which can take a while.

```sh
git clone https://github.com/taktekhq/lebanese-legislation.git
cd lebanese-legislation
./start
```

`./start` generates separate random owner and reader passwords in a private `.env` file. On an empty volume the DB image resolves the selected release, verifies its checksum and restores through the existing verified importer. API and MCP start only after the DB has a compatible restore receipt. Existing credentials are preserved.

Default startup follows the published `releases/latest.json` pointer. To use a prepared local release, place its valid `manifest.json` and referenced dump in a directory, create `.env` with `POSTGRES_PASSWORD` and `READER_PASSWORD` (or run `./start` once), then run:

```sh
LEBLEG_RELEASE_DIR=/absolute/path/to/release docker compose -f compose.yaml -f compose.local.yaml up -d --wait --wait-timeout 7200
```

For a published release, set `DATASET_REVISION=<artifact-commit>` and `MANIFEST_PATH=releases/<release-id>/manifest.json` in `.env` before startup. No data, embeddings or dump is baked into these images.

Open the website at <http://localhost:8080>, the API at <http://localhost:8000/api/v1/>, connect an MCP client to <http://localhost:8001/mcp>, or search:

```sh
curl --get 'http://localhost:8000/api/v1/search/' --data-urlencode 'q=قانون العمل'
```

Exact text and citation searches work without an AI key. To turn on search by meaning, add your own `GEMINI_API_KEY` to `.env`, then run:

```sh
docker compose up -d --force-recreate api mcp web
```

Arabic, English and French queries can retrieve multilingual source passages. English discovery titles are unofficial aids, not authoritative translations. Availability of a language depends on the source material.

All published ports bind to your own computer. The API has no user authentication and is meant for local use. Before exposing it publicly, set `RATE_LIMIT_PER_MINUTE` (LebaneseLegislation.com uses 60) or limit at your reverse proxy, and add HTTPS and an appropriate cost limit.

Stop services with `docker compose down`. This preserves the database and its release cache. Volume lifecycle belongs to the operator. There is no automatic refresh. Use a different Compose project and fresh volumes to select a new snapshot.

A failed first import never creates a readiness receipt. Restarting an initialized volume does not rerun init scripts or make a partial import healthy. After correcting a missing artifact or other restore error, an operator can explicitly retry the same selected release with `docker compose exec --user postgres db /opt/venv/bin/python /opt/lebleg/db/release.py init`. The importer refuses unrelated occupied databases. Never delete a volume containing data you need.

## What you get

| Artifact | Purpose |
| --- | --- |
| Existing `data/*.parquet` on Hugging Face | Portable source tables for analysis and other databases |
| `releases/<release-id>/corpus.dump` | PostgreSQL custom-format snapshot, public tables, normalized documents and saved vectors |
| `releases/<release-id>/manifest.json` | Source commit, counts, embedding settings, versions, licence and SHA-256 |
| `releases/latest.json` | Pointer to the latest verified database release |
| `ghcr.io/taktekhq/lebanese-legislation-db:0.4.0` | PostgreSQL 18.6, pgvector 0.8.2 and first-boot restore |
| `ghcr.io/taktekhq/lebanese-legislation-api:0.4.0` | The REST API (`/api/v1/`) |
| `ghcr.io/taktekhq/lebanese-legislation-mcp:0.4.0` | The MCP server (`/mcp`) |
| `ghcr.io/taktekhq/lebanese-legislation-web:0.4.0` | The website |

Older version tags stay published. See [changes](changes.md).

Database data is downloaded separately, not baked into the Docker image. The dump stores index definitions; PostgreSQL constructs the HNSW index during restore. It does not contain a portable copy of PostgreSQL's physical index files.

The release is built only from the pinned public Parquet files. It does not contain private website tables, user queries, analytics, credentials or website-only explanations. Exported views become snapshot tables. `source_documents` is a derived read model for this immutable snapshot; editing raw tables does not automatically update it or its vectors. Article retrieval follows the exported in-force view at the stated date; the raw tables retain the wider historical corpus.

When present, `GET /api/v1/law/{id}/article/{number}/` and MCP `get_article` include the additive `calculated_current` object for Civil Procedure (law 244565) amounts updated by decree 330819. It is returned only when the shown text is exactly the in-force source, its SHA-256 matches and its authority and multiplier are valid. Original text and versions keep their fields. Older snapshots without the derived table omit the object.

## How retrieval works

1. Search matches original text, titles and explicit citations in PostgreSQL first: `GET /api/v1/search/` and MCP `search_legislation`.
2. With `semantic=true` (API) or `semantic: true` (MCP), and a `GEMINI_API_KEY` on the server, Gemini embeds the question with `gemini-embedding-001` (768 dimensions) and PostgreSQL compares it with the saved passage vectors in `semantic_chunks` (`halfvec(768)`). Found passages come back as `related`. Without a key the result says `"semantic": "unavailable"`. Adding `select=true` lets Gemini keep only the passages that answer the question; it returns passage IDs, never wording.
3. Every passage is reloaded from the source text and hash-checked before it is returned, with a link to its source. The website's search page also uses Gemini to rewrite a question into Arabic search terms.

A missing explicit citation is not silently replaced with a nearby semantic match. An absent or failed provider leaves ordinary search available. Retrieval can miss relevant material or return irrelevant passages. Source links and exact quotations make checking possible; they do not establish legal correctness or current legal status. Scanned ruling pages keep their extraction provenance and confidence fields. OCR can contain errors; consult the linked originals.

The API key stays in the server container. It is never sent to the browser or stored in the public release. Search by meaning sends the question to Google, not database credentials. Do not send confidential queries without considering your provider's terms.

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
| `DATASET_REPO` | `taktekhq/lebanese-legislation` | Public Hub dataset repository |
| `DATASET_REVISION` | `main` | Resolved once to an immutable artifact commit |
| `MANIFEST_PATH` | `releases/latest.json` | Pointer or versioned manifest path |
| `GEMINI_API_KEY` | empty | Your key; turns on search by meaning |
| `GEMINI_MAX_CALLS_PER_DAY` | `500` | Provider calls per UTC day for each service; `0` turns search by meaning off |
| `GEMINI_CONCURRENCY` | `2` | Provider calls at once, 1 to 16 |
| `GEMINI_QUERY_CACHE_SIZE` | `256` | Cached answers per cache, up to 256; `0` turns caching off |
| `GEMINI_QUERY_CACHE_TTL` | `3600` | Seconds an answer stays cached, up to 3600; `0` turns caching off |
| `RATE_LIMIT_PER_MINUTE` | `0` | Requests a minute per client IP for the API and MCP; `0` is no limit. Set it when you host them publicly |
| `RATE_LIMIT_BURST` | `20` | Requests a client may send at once above that rate |
| `MCP_ALLOWED_ORIGINS` | empty | Comma-separated browser origins the MCP server answers, beyond its own localhost address. Clients that send no `Origin` are always answered |
| `GEMINI_SEARCH_MODEL` | `gemini-3.8-flash` | The model that rewrites questions on the website's search page |
| `GEMINI_EMBEDDING_MODEL` | `gemini-embedding-001` | Must match the saved vectors |

The daily cap, the cache and the concurrency limit live in each service process. Restarting resets the counter, and API, MCP and website each have their own. Use provider billing limits for a durable spending ceiling.

To pin a release, set `MANIFEST_PATH=releases/<release-id>/manifest.json` and `DATASET_REVISION=<artifact-commit>` before the first start. Updating those settings does not replace an already restored database. Use a separate Compose project and fresh volumes for a new snapshot.

## Use your own PostgreSQL

Use PostgreSQL 18.6 and pgvector 0.8.2 with a PostgreSQL 18 client. Create a fresh database and a nonprivileged login role named `lebleg_reader`; keep its password separate from the owner. Restore with the owner, then run the services as the reader:

```sh
python3 -m venv .venv
. .venv/bin/activate
pip install ./tools
# Set DATABASE_URL privately in your environment, without putting passwords in shell history.
lebleg restore --repo taktekhq/lebanese-legislation --revision main \
  --manifest-path releases/latest.json --cache-dir ./downloads
# Then, as lebleg_reader:
go run ./api    # or ./mcp, ./web
```

The DB and publisher images include PostgreSQL clients; the API, MCP and website images do not. For a standalone restore, run the DB image with `--entrypoint lebleg`, or install matching PostgreSQL clients alongside the CLI. `lebleg restore --help` also supports local manifest/dump inputs. The restorer verifies versions, checksum, counts, source/chunk integrity, HNSW index and reader privileges, adds what the services read ([`web.sql`](../tools/src/lebleg/sql/web.sql)) and records a restore receipt. Interrupted imports and post-import checks can be retried with the same manifest. It never drops unrelated tables. Direct `pg_restore` is possible, but skips those checks and the service tables.

## Rebuild from public Parquet

Publisher work needs more memory and disk than consuming a release. Use a separate empty build database. Do not point this command at a production database. Install `pip install './tools[build]'`, download `data/*.parquet` from a pinned Hub commit, then:

```sh
lebleg build --snapshot-dir ./snapshot/data --revision <40-character-source-commit> \
  --release-id <new-release-id> --output-dir ./artifacts/release
```

`DATABASE_URL` identifies the empty target. Optional `REUSE_DATABASE_URL` identifies a read-only source of compatible `semantic_chunks`; only vectors whose hashes and passage boundaries match the public source text are reused. Without complete coverage the command writes a coverage report and exits with status 2, without publishing a release.

Explicitly opt in to paid document embeddings with `--embed-missing --max-new-embeddings 10000` and your own `GEMINI_API_KEY`. The cap is checked before calls; progress is committed in batches, and `--resume` continues the same build. No automatic unrestricted embedding job runs. Use `--as-of-date YYYY-MM-DD` for reproducible date-dependent source selection.

For a larger explicitly budgeted build, optional `--embedding-workers` (1 to 8) and `--embedding-batch-size` (1 to 100) provide bounded concurrency; defaults are 1 and 32. Database writes stay in one thread. Completed batches are retained after a failure, and a retry skips validated saved vectors. Set the provider's daily call limit and concurrency explicitly for such jobs.

`--max-input-tokens` adds a durable input-token reservation budget. It calls Gemini's token counter before each paid batch and reserves that count plus 16 tokens per document for task overhead. The default ledger is `embedding-budget.json` inside the output directory; override it with `--embedding-budget-ledger`. Preserve it when resuming. Failed or interrupted requests retain reservations, and changing its model or ceiling is refused. This guard is separate from the provider invoice and account-wide billing controls.

Publish a completed, checksum-verified release with your own authorized `HF_TOKEN`:

```sh
lebleg publish --directory ./artifacts/release --repo your-account/your-dataset
```

Publishing adds an immutable versioned directory and updates the latest pointer in one Hub commit. It preserves Parquet files and refuses to overwrite an existing release ID. Keep the token in the environment. Code release tags and dataset source/artifact commits are distinct identifiers.

## Reuse vectors in an existing serving index

`lebleg seed` copies only missing or invalid compatible vector rows from a released database into an existing independent `semantic_chunks` index. It reads the current canonical documents separately and checks their reference, full-text SHA-256, exact passage text and Unicode boundaries before copying anything. It makes no model requests, imports no generation markers and does no pruning.

Set `SEED_SOURCE_DATABASE_URL`, `SEED_CANONICAL_DATABASE_URL` and `SEED_TARGET_DATABASE_URL` privately. Source and canonical connections are read-only. All three databases must use the same stable document references and compatible vector schema. Start with a bounded pilot:

```sh
lebleg seed --group-size 100 --limit 25
```

By default canonical documents come from `source_documents`. `--canonical-query-file ./canonical.sql` accepts an operator-controlled SELECT returning `kind, source_id, part, lang, title, text`. `--limit` and `--offset` count canonical documents; zero limit means the whole supplied query. Existing valid vectors and unrelated sources are preserved, and batches are committed so a retry can skip completed work. Unavailable or mismatched source vectors are reported, without API fallback.

Use this for an independent serving index. A versioned restored snapshot remains immutable. The serving application must validate hits against the same canonical source or refresh its own read model; seeding does not update `source_documents`. Serialize it with your corpus replacement process. It takes the model advisory lock used by the compatible indexer, but filesystem or deployment locks remain the operator's responsibility.

## Build images and develop

Every image builds from the repository root:

```sh
docker build -f db/Dockerfile -t lebleg-db-local .
docker build -f api/Dockerfile -t lebleg-api-local .
docker build -f mcp/Dockerfile -t lebleg-mcp-local .
docker build -f web/Dockerfile -t lebleg-web-local .
# Maintainers and contributors only: optional snapshot publisher dependencies.
docker build -f db/Dockerfile --target publisher -t lebleg-publisher-local .
go vet ./... && go test ./...
cd tools && pip install '.[build,test]' && pytest -q && ruff check src tests
```

Go tests that need a database use the fictional fixture in [`web/`](../web/README.md). Python tests that need PostgreSQL create and remove uniquely named databases when `LEBLEG_TEST_ADMIN_DSN` is set. Container tests run when `LEBLEG_TEST_IMAGE` is set, and the Compose test when the four `LEBLEG_TEST_*_IMAGE` variables are set. They never use your existing database. See the [checks workflow](../.github/workflows/ci.yml) for a disposable setup. `docker build -f db/Dockerfile --target postgres` gives a plain PostgreSQL with pgvector for those tests.

The DB image pins the official `postgres:18.6` base by digest and verifies the pgvector source checksum. The image publication workflow builds the four images for `linux/amd64,linux/arm64`, checks each one on both platforms against a fictional database, and refuses to overwrite version tags. The optional publisher stays separate.

Set `LEBLEG_TEST_DB_IMAGE` to a locally built DB target to run the synthetic first-boot, restart and failure integration checks. These use tiny fixture dumps and disposable volumes, never the real dataset.

## Licence and attribution

Code: [MIT](../LICENSE), copyright Taktek and Nizar Mahmoud.

The dataset has its own licence, `other: public-texts-credit-the-centre`. Credit the **Centre for Research and Studies in Legal Informatics, Lebanese University** for the legal texts. Dataset structure: Taktek, LLC. The code's MIT licence does not replace the dataset's terms. Preserve source provenance and the distinction between Arabic legal texts and unofficial discovery aids.
