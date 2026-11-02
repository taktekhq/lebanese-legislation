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
Semantic search uses Cloudflare Vectorize index `lebleg-chunks`: 1,547,937 chunks,
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

## Origin deployment

`workers/static-site` serves R2 content and forwards origin requests through
service bindings to `lebleg-origin-blue` or `lebleg-origin-green`.
These are independent environments in `workers/origin`, each with its own
`Origin` Durable Object and container instance named `main`. Origin Workers have
no public routes. Deploying one side cannot restart the other side.

The `origin` workflow builds the site image from the data image, reads
`_build/live-side` from R2 (default blue), then deploys only the idle origin
Worker and sets its Gemini and Vectorize secrets. Authenticated checks through
the site Worker warm that side. Switching writes its name to the R2 flag and
waits 20 seconds for the site Worker's 15 second memory cache. Verification
failure writes the old name back; success stops the old container through the
authenticated restart endpoint. Neither switch nor rollback deploys a Worker.
The named probe User-Agent and public verification checks stay the same.

`deploy_site_worker` defaults to false. When true, a separate job deploys the
site Worker before staging, applying the `route` and `mode` inputs. The `image`
and `data_tag` inputs control the image build as before.

For the first migration, deploy both origin environments with the current image
and set their secrets before deploying the new site Worker. Warm and check each
through a temporary local probe using remote service bindings, set the R2 flag
to blue, then deploy the site Worker with the existing routes and intended mode.
The selected replacement must already be warm because that site deployment
deletes the old container classes. See the [container deploy guide](../workers/static-site/container/README.md)
for the job sequence and local checks.

The [Hugging Face dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation)
provides source tables, PostgreSQL releases and the openly published
[vectors Parquet](https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet).
The vectors file has columns `id`, `document`, `model`, `hash` and `values`
(`FLOAT[768]`). See the [service reference](reference.md#vectors) for a DuckDB
example. Local Compose services and their downloadable PostgreSQL snapshots
are separate from the hosted container image, which includes its database.
