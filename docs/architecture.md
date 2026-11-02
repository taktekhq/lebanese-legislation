# Architecture

Current setup after the changes of 1-2 November 2026. The former private
`taktekhq/lebaneselegislation` repository is folded into this public repository:
code, scraper, build pipeline, website and MCP.

## Request path

A visitor requests lebaneselegislation.com through Cloudflare. The JavaScript
Worker in [`workers/static-site`](../workers/static-site/) serves a static page
from the R2 bucket `lebleg-site`, or sends a dynamic request to one Cloudflare
Container. Ruling PDFs are also served from R2 by the same Worker.

Search pages, `/api/v1/`, filters and Gazette scans go to the container.
Its [Dockerfile](../workers/static-site/container/Dockerfile) packages
PostgreSQL 18.6 with the database baked into the image and the Go website server.
Semantic search uses Cloudflare Vectorize index `lebleg-chunks`: 1,433,051 chunks,
768 dimensions, model `gemini-embedding-001`. The vectors have moved out of
PostgreSQL into Vectorize.

## Build path

`data/` is the input for the site build. GitHub Actions renders about 760,000
static pages through `cmd/static-export` in 8 parallel shards and uploads them
to `lebleg-site`. A separate workflow builds and deploys the container image.
The vectors workflow exports the saved vectors, publishes Parquet on Hugging
Face and loads Vectorize. Later syncs upload only changed vectors.

| Workflow | Output |
| --- | --- |
| [static-site.yml](../.github/workflows/static-site.yml) | `data/` to static pages in R2, using `cmd/static-export` and 8 parallel shards. |
| [origin.yml](../.github/workflows/origin.yml) | Container image with the database and Go website server, deployed to Cloudflare Containers. |
| [vectors.yml](../.github/workflows/vectors.yml) | `build` then `publish` and `vectorize`: `cmd/export-vectors` exports vectors, Hugging Face receives `vectors/vectors.parquet`, and `cmd/vectorize-sync` loads `lebleg-chunks`. |
| [data.yml](../.github/workflows/data.yml) | `data/` to the source-table Parquet files on Hugging Face. |

The [Hugging Face dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation)
provides source tables, PostgreSQL releases and the openly published
[vectors Parquet](https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet).
The vectors file has columns `id`, `document`, `model`, `hash` and `values`
(`FLOAT[768]`). See the [service reference](reference.md#vectors) for a DuckDB
example. Local Compose services and their downloadable PostgreSQL snapshots
are separate from the hosted container image, which includes its database.
