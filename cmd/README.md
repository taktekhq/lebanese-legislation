# Commands

One command per job. Each reads the database named by `-db` (a libpq
connection string; default `LEBLEG_DSN`, else the local socket) and prints
its own flags with `-h`. Install one with
`go install github.com/taktekhq/lebanese-legislation/cmd/<name>@latest`.

Commands marked **key** need an API key or a signed-in model CLI; the rest
run on public data only.

## Build the database

| Command | What it does |
| --- | --- |
| `build-db` | Builds the database from `archive/` from scratch and reports what it loaded. `-only` reruns one step. |
| `diff-build` | Builds two git refs side by side and reports how the database changed. |
| `review-scans` | Shows how each text read from Gazette scans will be cut and loaded, before a release. |
| `load-official` | Loads texts published from official Gazette scans. |
| `ul-index` | Lists the texts the Centre published from a given year. |

## Model readings (key)

| Command | What it does |
| --- | --- |
| `cite` | Asks Gemini which texts and articles each amending article changes (`GEMINI_API_KEY`). `-report` and `-preview` need no key. |
| `citeagy` | Reads amending and citing articles, and rulings, through the agy CLI, the claude CLI or the Claude API. |
| `judge` | Asks a model to judge each recorded change against the amending text (`GEMINI_API_KEY`, or `-agy`, or `-claude`). |
| `ruling-files` | Downloads the full judgments (PDF scans) behind ruling pages. No key. |
| `ruling-read` | Transcribes the downloaded judgments through the agy CLI. |
| `ruling-ocr` | Routes each judgment page to Tesseract, Claude or Gemini and reads it (`-stage gemini` needs `GEMINI_API_KEY`, `-stage claude-api` needs `ANTHROPIC_API_KEY`). |

## Check the data

| Command | What it does |
| --- | --- |
| `check-wording` | The wording guard: every law text must be a span of its archived source. Exit 4 when one is not. |
| `record-scan-manifest` | Records the SHA-256 of each new scan bundle file, so the wording guard can tell a changed file. |
| `check-amounts`, `check-dates`, `check-versions`, `check-citations` | Checks of the built data against pinned expectations. |
| `check-rulings` | Checks the ruling files, their readings and the reading spend. |
| `recall-report` | Compares articles with an amending verb to the amendment rows built from them; exit 1 when the ratio falls. |

## Write it out

| Command | What it does |
| --- | --- |
| `export-data` | Writes the database into `data/` as sorted text, after the wording guard passes. |
| `calculated-current` | Writes each article's source-verified calculated wording to `data/article_calculated_current.jsonl`. |
| `export-graph` | Writes the citation graph as CSV and GraphML. |

## Get the database

Use the [`-db` or `-all` image](../README.md#images) for PostgreSQL, or the
[Parquet tables on Hugging Face](https://huggingface.co/datasets/taktekhq/lebanese-legislation) for portable data.
The legacy release commands remain for the Dockerfile's contributor targets
and image checks. They are not the public data distribution.

## The website

The server itself is [`web/`](../web/README.md) (with `api/` and `mcp/`); these are its tools.

| Command | What it does |
| --- | --- |
| `usage-stats` | Adds up one day of the anonymous usage record the web server keeps with `-events`, as JSON. |
| `journal-sitemap` | Prints the project journal's sitemap from the posts built into the site. No database. |
| [`export-vectors`](export-vectors/) | Streams saved semantic chunks to NDJSON, including 768-dimensional vectors and stable chunk IDs. |
| [`vectorize-sync`](vectorize-sync/) | Upserts changed vectors and deletes removed IDs in Cloudflare Vectorize, using an id/hash manifest. **key**: `LEBLEG_VECTORIZE_TOKEN`. |
| `searchindex` | Builds the semantic search index in its own pgvector database (`GEMINI_API_KEY`). **key** |
| `topicgen` | Writes and checks the topic pages with a model that sees only the cited articles (claude, codex or agy CLI). **key** |
| `check-english` | Checks that English stays an unofficial aid: flagged surfaces, English titles for new texts, no Latin article bodies. |

## Hosted builds

[static-site.yml](../.github/workflows/static-site.yml) runs `static-export` in
24 parallel shards into R2, rendering only the pages whose data changed. [origin.yml](../.github/workflows/origin.yml) deploys
the database and Go website server in one Cloudflare Container.
[vectors.yml](../.github/workflows/vectors.yml) uses `export-vectors` and
`vectorize-sync` to publish `vectors/vectors.parquet` on Hugging Face and load
Vectorize index `lebleg-chunks`. See [architecture](../docs/architecture.md).
