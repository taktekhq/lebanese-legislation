# Lebanese Legislation Starter

> The first database release is being validated. Parquet and initial container images are available. The setup below depends on the pending database manifest.

Restore the public [Lebanese Legislation dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation) into PostgreSQL with a precomputed pgvector index. Run an API that returns exact matches first and related source passages underneath.

The existing Parquet dataset remains the portable data distribution. This project adds a PostgreSQL snapshot and reusable search service. No model training is required.

## Start locally

Install Docker with Compose. Allow at least 4 GB of available memory and 30 GB of free disk space; 8 GB of memory is recommended. The first start downloads the full release and rebuilds its indexes, which can take a while.

```sh
git clone https://github.com/taktekhq/lebanese-legislation-starter.git
cd lebanese-legislation-starter
./start
```

`./start` generates separate random database owner and reader passwords in a private `.env` file, downloads a pinned release, verifies its checksum, restores into a fresh database, checks its contents, then starts the API. Existing credentials are preserved. It refuses to replace an unrelated occupied database.

Open <http://localhost:8000/docs> or search:

```sh
curl --get 'http://localhost:8000/search' --data-urlencode 'q=قانون العمل'
```

Exact text and citation searches work without an AI key. To enable semantic retrieval, add your own `GEMINI_API_KEY` to `.env`, then run:

```sh
docker compose up -d --force-recreate api
```

Arabic, English and French queries can retrieve multilingual source passages. English discovery titles are unofficial aids, not authoritative translations. Availability of a language depends on the source material.

All published ports bind to your own computer. The API has no user authentication and is intended as a local starter. Before exposing it publicly, add authentication or rate limiting, HTTPS and an appropriate cost limit at your reverse proxy.

Stop services with `docker compose down`. This preserves the database and downloaded release. Deleting the Compose volumes removes those local copies.

## What you get

| Artifact | Purpose |
| --- | --- |
| Existing `data/*.parquet` on Hugging Face | Portable source tables for analysis and other databases |
| `releases/<release-id>/corpus.dump` | PostgreSQL custom-format snapshot, public tables, normalized documents and saved vectors |
| `releases/<release-id>/manifest.json` | Source commit, counts, embedding settings, versions, licence and SHA-256 |
| `releases/latest.json` | Pointer to the latest verified database release |
| `ghcr.io/taktekhq/lebanese-legislation-starter:postgres-18.6-vector0.8.2` | PostgreSQL 18.6 with pgvector 0.8.2 |
| `ghcr.io/taktekhq/lebanese-legislation-starter:0.1.3` | Restore CLI and search API |
| `ghcr.io/taktekhq/lebanese-legislation-starter:publisher-0.1.3` | Optional tools for rebuilding and publishing snapshots |

Database data is downloaded separately, not baked into the Docker image. The dump stores index definitions; PostgreSQL constructs the HNSW index during restore. It does not contain a portable copy of PostgreSQL's physical index files.

Calculated current monetary wording, when exported by the canonical dataset, is available as `source_documents.metadata.calculated_current` and the `article_calculated_current` table. Literal search also matches calculated text and amounts. Returned passage text stays verbatim. Use the labelled object for current monetary answers and cite its authority. Older snapshots without the optional sidecar make no calculated claims. See [the release and compatibility guide](docs/calculated-current-release.md).

The release is built only from the pinned public Parquet files. It does not contain private website tables, user queries, analytics, credentials or website-only explanations. Exported views become snapshot tables. `source_documents` is a derived read model for this immutable snapshot; editing raw tables does not automatically update it or its vectors. Article retrieval follows the exported in-force view at the stated date; the raw tables retain the wider historical corpus.

## How retrieval works

1. The server searches original text and explicit citations first.
2. If enabled, Gemini embeds the query using `gemini-embedding-001`, with 768 dimensions and `RETRIEVAL_QUERY`. PostgreSQL compares it with saved document vectors, stored as `halfvec(768)`.
3. Returned passages are copied from validated source text and carry source links. Optional `select=true` lets Gemini choose passage IDs; it cannot supply replacement legal wording.

A missing explicit citation is not silently replaced with a nearby semantic match. An absent or failed provider leaves ordinary search available. Retrieval can miss relevant material or return irrelevant passages. Source links and exact quotations make checking possible; they do not establish legal correctness or current legal status. Scanned ruling pages retain extraction provenance and confidence fields. OCR can contain errors; consult the linked originals.

The API key stays in the server container. It is never sent to the browser or stored in the public release. Semantic search sends the query to Google; optional selection also sends candidate excerpts. No MCP server or database login is given to the model. Do not send confidential queries without considering your provider's terms.

Precomputed vectors save the cost of embedding the whole corpus. Query embeddings still require a compatible Gemini model. Another embedding model needs a separately rebuilt document index, even if it also has 768 dimensions.

## Configuration

Copy the optional settings from `.env.example`. `./start` creates required passwords when `.env` does not exist; an existing manually created `.env` must contain both passwords.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DB_PORT` | `5433` | Loopback PostgreSQL port |
| `API_PORT` | `8000` | Loopback API port |
| `DB_MEMORY` | `3g` | Database container cap; index construction needs headroom |
| `DATASET_REPO` | `taktekhq/lebanese-legislation` | Public Hub dataset repository |
| `DATASET_REVISION` | `main` | Resolved once to an immutable artifact commit |
| `MANIFEST_PATH` | `releases/latest.json` | Pointer or versioned manifest path |
| `GEMINI_MAX_CALLS_PER_DAY` | `500` | Attempted provider HTTP calls per UTC day; `0` disables them |
| `GEMINI_CONCURRENCY` | `2` | Simultaneous provider requests, range 1 to 16 |

The provider budget and cache are process-local. The supplied deployment runs one API worker. Restarting resets the counter; replicas each have their own allowance. Use provider billing limits for a durable spending ceiling. Failed provider calls count toward the daily cap. Query embedding cache defaults to 256 entries for one hour, bounded to these maxima; configure `GEMINI_QUERY_CACHE_SIZE` and `GEMINI_QUERY_CACHE_TTL` on a custom deployment. Set either to zero to disable caching.

To pin a release, set `MANIFEST_PATH=releases/<release-id>/manifest.json` and `DATASET_REVISION=<artifact-commit>` before the first start. Updating those settings does not replace an already restored database. Use a separate Compose project and fresh volumes for a new snapshot.

## Use your own PostgreSQL

Use PostgreSQL 18.6 and pgvector 0.8.2 with a PostgreSQL 18 client. Create a fresh database and a nonprivileged login role named `lebleg_reader`; keep its password separate from the owner. Restore with the owner, then connect the API as the reader:

```sh
python3 -m venv .venv
. .venv/bin/activate
pip install .
# Set DATABASE_URL privately in your environment, without putting passwords in shell history.
lebleg restore --repo taktekhq/lebanese-legislation --revision main \
  --manifest-path releases/latest.json --cache-dir ./downloads
lebleg serve
```

The supplied image includes the required clients. `lebleg restore --help` also supports local manifest/dump inputs. The restorer verifies versions, checksum, counts, source/chunk integrity, HNSW index and reader privileges before recording a restore receipt. Interrupted imports and post-import checks can be retried with the same manifest. It never drops unrelated tables. Direct `pg_restore` is possible, but bypasses those checks and does not create the receipt required by this API.

## Rebuild from public Parquet

Publisher work needs more memory and disk than consuming a release. Use a separate empty build database. Do not point this command at a production database. Install `pip install '.[build]'`, download `data/*.parquet` from a pinned Hub commit, then:

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

`lebleg seed` copies only missing or invalid compatible vector rows from a released starter database into an existing independent `semantic_chunks` index. It reads the current canonical documents separately and checks their reference, full-text SHA-256, exact passage text and Unicode boundaries before copying anything. It makes no model requests, imports no generation markers and does no pruning.

Set `SEED_SOURCE_DATABASE_URL`, `SEED_CANONICAL_DATABASE_URL` and `SEED_TARGET_DATABASE_URL` privately. Source and canonical connections are read-only. All three databases must use the same stable document references and compatible vector schema. Start with a bounded pilot:

```sh
lebleg seed --group-size 100 --limit 25
```

By default canonical documents come from `source_documents`. `--canonical-query-file ./canonical.sql` accepts an operator-controlled SELECT returning `kind, source_id, part, lang, title, text`. `--limit` and `--offset` count canonical documents; zero limit means the whole supplied query. Existing valid vectors and unrelated sources are preserved, and batches are committed so a retry can skip completed work. Unavailable or mismatched source vectors are reported, without API fallback.

Use this for an independent serving index. A versioned restored snapshot remains immutable. The serving application must validate hits against the same canonical source or refresh its own read model; seeding does not update `source_documents`. Serialize it with your corpus replacement process. It takes the model advisory lock used by the compatible indexer, but filesystem or deployment locks remain the operator's responsibility.

## Build images and develop

```sh
# Build the database image first, then the runtime against it.
docker build -f docker/postgres.Dockerfile -t lebleg-postgres-local .
docker build --target runtime --build-arg POSTGRES_IMAGE=lebleg-postgres-local -t lebleg-starter-local .
pip install '.[build,test]'
pytest -q
ruff check src tests
```

Tests that need PostgreSQL create and remove uniquely named databases when `LEBLEG_TEST_ADMIN_DSN` is set. Container tests run when `LEBLEG_TEST_IMAGE` is set. They do not use your existing database as a test target. See the CI workflow for a disposable setup.

The PostgreSQL image pins its base digest and verifies the pgvector source checksum. Initial published images target Linux amd64. Building locally is available for other platforms; an Apple Silicon machine may also run amd64 images through Docker emulation.

## Licence and attribution

Code: [MIT](LICENSE), copyright Taktek and Nizar Mahmoud.

The dataset has its own licence, `other: public-texts-credit-the-centre`. Credit the **Centre for Research and Studies in Legal Informatics, Lebanese University** for the legal texts. Dataset structure: Taktek, LLC. The code's MIT licence does not replace the dataset's terms. Preserve source provenance and the distinction between Arabic legal texts and unofficial discovery aids.
