# Lebanese Legislation

The code and data behind [LebaneseLegislation.com](https://lebaneselegislation.com/): 103,117 laws, 389,078 articles, 47,657 court rulings and 6,851 Official Gazette issues, with search, a REST API and an MCP server. Everything is open: the code here, the data on [Hugging Face](https://huggingface.co/taktekhq), and the images on GitHub Container Registry.

The former private `taktekhq/lebaneselegislation` repository is folded into this
one: code, scraper, build pipeline, website and MCP.

## Hosting

Cloudflare serves about 760,000 static pages from the R2 bucket `lebleg-site`
through the JavaScript Worker in [`workers/static-site`](workers/static-site/).
The same Worker serves ruling PDFs from R2. Search pages, `/api/v1/`, filters,
Gazette scans and `/mcp` go through `workers/origin` to the live blue/green
Cloudflare Container, running the `-all` image with PostgreSQL 18.6, the
database baked into the image and the Go website server.

[static-site.yml](.github/workflows/static-site.yml) renders `data/` with
`cmd/static-export` in 24 parallel shards. [origin.yml](.github/workflows/origin.yml)
deploys the container. [vectors.yml](.github/workflows/vectors.yml) exports and
publishes vectors and loads Cloudflare Vectorize index `lebleg-chunks`.
See [architecture](docs/architecture.md) for the request and build paths.

## Update the data yourself

Run `scripts/nightly.sh` on your own machine to scrape, rebuild, check and
export the data, then open a data-only pull request. Use `--dry-run` to do the
work locally without pushing or opening a PR. GitHub Actions cannot run it.

You need Bash, flock, Git, rsync, curl, Python 3, Go, PostgreSQL and an
authenticated GitHub CLI. Set `LEBLEG_DSN` to a dedicated rebuild database;
`LEBLEG_STORE`, `LEBLEG_DATA` and `LEBLEG_LOCK_FILE` override local paths.
`LEBLEG_REPO` selects the target repository. Maintainers merge the PR, then
the workflows publish everything. See [Update the data](docs/update-data.md)
for requirements, every step, configuration and a nightly user systemd timer.

## Where everything is

| You want | Go to |
| --- | --- |
| Read the laws | [LebaneseLegislation.com](https://lebaneselegislation.com/) |
| Connect an AI assistant | The free MCP server at `https://lebaneselegislation.com/mcp`. [Setup](docs/mcp.md) |
| The data as tables | [Parquet files on Hugging Face](https://huggingface.co/datasets/taktekhq/lebanese-legislation) |
| The data as a PostgreSQL database | [PostgreSQL dumps on Hugging Face](https://huggingface.co/datasets/taktekhq/lebanese-legislation/tree/main/postgres) (`pg_restore`, no embeddings), or the `-db` or `-all` image below, with both databases baked from `data/`. |
| Semantic search vectors | [vectors/vectors.parquet](https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet), 1,547,937 chunks, 768 dimensions, `gemini-embedding-001`. [Read with DuckDB](docs/reference.md#vectors). |
| The original court scans | [Court scans on Hugging Face](https://huggingface.co/datasets/taktekhq/lebanese-court-scans) |
| Every source page as it was served | [Archive dataset on Hugging Face](https://huggingface.co/datasets/taktekhq/lebanese-legislation-archive) and [its repository](https://github.com/taktekhq/lebanese-legislation-archive) |
| What changed and why | The [project journal](https://lebaneselegislation.com/blog/) and [docs/changes.md](docs/changes.md) |
| Get paid to study with it (students in Lebanon) | [The student program](#students-get-paid-to-study-with-it) |
| Everything on your own machine | [Run the services locally](#run-the-services-locally) |

Data lives on Hugging Face. Code and Docker images live here. Each GitHub release is a code version, never a data download.

## Production and development

The same image, `ghcr.io/taktekhq/lebanese-legislation-all`, runs in both. It holds PostgreSQL with the data, the API (`/api/v1/`), the MCP server (`/mcp`) and the website server.

| | Production | Development |
|---|---|---|
| Static pages | built by `static-site.yml` into Cloudflare R2, served by the Worker in `workers/static-site` | rendered on request by the `-all` image |
| API, MCP, search | the Worker passes them to `-all` (Cloudflare Containers, blue/green, `workers/origin`) | `-all` directly |
| Run it | `origin.yml` deploys it | `docker compose --profile all up all`, then open http://localhost:8080 |

In development every page is rendered on request, which is slower than production but needs nothing besides the image. To get the exact static copy production serves, run `static-export` against the database: see [docs/static-export.md](docs/static-export.md).

## Run the services locally

```sh
git clone https://github.com/taktekhq/lebanese-legislation.git
cd lebanese-legislation
./setup
docker compose --profile all up -d all
```

You need Docker Compose, 8 GB of free memory and 30 GB of free disk. `./setup` writes private local passwords to `.env`. The image already holds both databases. The first start copies them into an empty volume; later starts reuse it.

Then open the website at http://localhost:8080, the API at http://localhost:8080/api/v1/, or point an MCP client at http://localhost:8080/mcp.

To run the database, API and MCP as separate services instead, use `docker compose up -d --wait db api mcp`: the API answers on http://localhost:8000/api/v1/ and MCP on http://localhost:8001/mcp. Every image is pulled as `:latest`, so a `docker compose pull` is all an update needs. Exact and citation search need no AI key. For search by meaning, configure your own Gemini key and Vectorize index, or load the public vectors into pgvector in `lebleg_search`. See [loading vectors](vectors/README.md).

## Images

All names use the prefix `ghcr.io/taktekhq/lebanese-legislation`.

| Image | Holds | Used by |
| --- | --- | --- |
| `-db` | PostgreSQL 18.6, pgvector, both databases baked from `data/`, chunk text and hashes without embeddings, optional scans | Compose, database users and the base of `-all` |
| `-api` | Standalone API server, unchanged Dockerfile and amd64/arm64 support | Compose and API users with a separate database |
| `-mcp` | Standalone MCP server, unchanged Dockerfile and amd64/arm64 support | Compose and MCP clients with a separate database |
| `-all` | `-db` plus the Go server for dynamic pages, `/api/v1/`, `/mcp` and badges | Compose, Cloudflare Containers and self-hosted sites |
| `-vectors` | `vectorize-sync`, `export-vectors`, DuckDB 1.5.6; downloads Hugging Face vectors at runtime | The vectors workflow and users loading Vectorize or pgvector |

Each image is built by the workflow that produces its inputs: the
[checks](.github/workflows/checks.yml) workflow publishes `-api`, `-mcp` and
`-all` after a push to main, [data](.github/workflows/data.yml) publishes `-db`
and `-all` after a data change, and [vectors](.github/workflows/vectors.yml)
publishes `-vectors`. [images.yml](.github/workflows/images.yml) is a manual
button to rebuild one. Every image gets a `latest` tag and a UTC date tag, so
use `:latest` unless you need a fixed copy. Static pages are published directly to R2.
Publishing of `-web` and `-data` has stopped; existing registry packages remain.
Compose pulls `ghcr.io/taktekhq/lebanese-legislation-all:latest` for the website, including its database, API and MCP.

API and MCP candidates are smoke-tested on both architectures before promotion;
existing immutable release versions are retained and new versions are added when free.

[Service reference](docs/reference.md): configuration and using your own PostgreSQL.

## What is in this repository

| Folder | What it is |
| --- | --- |
| [`data/`](data/) | The database as text: one folder per table, rows sorted by key, so `git log -p` shows what changed. `data/load.sh` builds it in PostgreSQL. |
| [`archive/`](https://github.com/taktekhq/lebanese-legislation-archive) | Every source page as the Centre's site served it, with its change history: [its own repository](https://github.com/taktekhq/lebanese-legislation-archive), mounted here as a submodule. A plain clone skips its 606,000 files; `git submodule update --init archive` fetches them. |
| [`db/`](db/) | The database's schema and the build's curated inputs (the Go package `db`), and the PostgreSQL image, built with pgvector on the official `postgres:18.6` image. |
| [`web/`](web/) | The website: its `main.go`, test-only Dockerfile, SQL schema and a fictional test database. |
| [`api/`](api/) | The REST API: its `main.go` and Dockerfile. |
| [`mcp/`](mcp/) | The MCP server: its `main.go` and Dockerfile. |
| [`internal/`](internal/) | The Go code: what the three services share (pages, API, MCP tools, search) and the build that makes the database from the source pages (`internal/build`, with parsing, amendments, citations, the wording guard in `internal/provenance`, and the export). |
| [`scraper/`](scraper/) | The crawler that reads the Lebanese University's site into the archive, and the scripts that publish the archive. |
| [`cmd/`](cmd/) | One command per job: build the database, model readings, data checks, export, and local services. [Listed here](cmd/README.md). |
| [`tools/`](tools/) | Only `tools/src/lebleg/sql/web.sql` now: the serving schema the database image applies for the website, API and MCP. |
| [`docs/`](docs/) | Architecture, service reference, MCP setup, CI and changes. |
| [`workers/`](workers/) | Cloudflare Workers, including the static-site router and its dynamic container. |

`compose.yaml` runs the four images. `compose.build.yaml` builds them from this checkout instead. `compose.local.yaml` selects an already built database image. `server.json` is the [MCP Registry](https://registry.modelcontextprotocol.io/) entry and `.mcp.json` the client configuration for the hosted server.

## Students: get paid to study with it

If you study law or political science at a Lebanese university right now, we pay
$10 for every week you study with the site (at least 2 days a week), paid monthly,
until March 2027. You tell us what is wrong, missing or hard to use, and fill in
a 10-minute form once a month. We chose to spend this money on students instead
of ads. [Apply on LinkedIn](https://www.linkedin.com/jobs/view/4483994893/).

## Contribute and licence

See [CONTRIBUTING.md](CONTRIBUTING.md).

Code is [MIT licensed](LICENSE), by Taktek and Nizar Mahmoud. The data has its own [terms and attribution](https://huggingface.co/datasets/taktekhq/lebanese-legislation#what-to-check): credit the Centre for Research and Studies in Legal Informatics, Lebanese University, for the texts. The code licence does not cover the legal texts or court scans. This is not legal advice or an official publication.
