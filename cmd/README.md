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
| `mcp-snapshot` | Writes a SQLite (D1) snapshot of the database for the hosted MCP Worker. |

## The PostgreSQL release

The dump on Hugging Face (`releases/<id>/corpus.dump`) that the db image
restores. These take `-db` from `DATABASE_URL`. The
[release workflow](../.github/workflows/release.yml) runs the build and
publish with their keys.

| Command | What it does |
| --- | --- |
| `restore-release` | Downloads a release (or takes a local manifest), checks its SHA-256, restores it into an empty PostgreSQL 18.6 with pgvector 0.8.2, checks every count, hash and index, applies `web.sql` and records a receipt. The db image runs it on first start. |
| `check-release` | Exit 1 unless the database holds one verified restored release. The db image's health check. |
| `build-release` | Builds a release from a database loaded from `data/`: reuses an earlier release's vectors, embeds the rest (**key**: `GEMINI_API_KEY`, only with `-embed-missing`, capped), validates, writes `corpus.dump` and `manifest.json`. Exit 2 when vectors are still missing. |
| `publish-release` | Uploads a built release to the Hugging Face dataset in one commit, with the `latest` pointer and the card's release block, then reads it back to check it (**key**: `HF_TOKEN`). |

