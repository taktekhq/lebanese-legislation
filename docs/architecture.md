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
to `lebleg-site`. The images workflow publishes containers; origin deploys the published `-all`.
The vectors workflow filters the public vectors to IDs generated from current `data/`,
publishes Parquet on Hugging Face and loads Vectorize through the vectors image.
Later syncs upload changed vectors and delete IDs absent from the current set.
Missing embeddings stop the workflow before publication or sync. The 21 stale
archive IDs found in the comparison are included in the deletion baseline.

| Workflow | Output |
| --- | --- |
| [static-site.yml](../.github/workflows/static-site.yml) | `data/` to static pages in R2, using `cmd/static-export` and 8 parallel shards. |
| [images.yml](../.github/workflows/images.yml) | Publishes all six images, with path selection and independent jobs. Scans and static pages come from R2. |
| [origin.yml](../.github/workflows/origin.yml) | Container image with the database and Go website server, deployed to Cloudflare Containers. |
| [vectors.yml](../.github/workflows/vectors.yml) | `build` then `vectorize` and `publish`: current chunk IDs filter the public Parquet, Hugging Face receives `vectors/vectors.parquet`, and the vectors image syncs `lebleg-chunks`. |
| [data.yml](../.github/workflows/data.yml) | `data/` to the source-table Parquet files on Hugging Face. |

## Origin deployment

`workers/static-site` serves R2 content and forwards origin requests through
service bindings to `lebleg-origin-blue` or `lebleg-origin-green`.
These are independent environments in `workers/origin`, each with its own
`Origin` Durable Object and container instance named `main`. Origin Workers have
no public routes. Deploying one side cannot restart the other side.

The `origin` workflow pulls the published all-in-one image and copies it to the Cloudflare registry, reads
`_build/live-side` from R2 (default blue), then deploys only the idle origin
Worker and sets its Gemini and Vectorize secrets. Authenticated checks through
the site Worker warm that side. Switching writes its name to the R2 flag and
waits 20 seconds for the site Worker's 15 second memory cache. Verification
failure writes the old name back; success stops the old container through the
authenticated restart endpoint. Neither switch nor rollback deploys a Worker.
The named probe User-Agent and public verification checks stay the same.

`deploy_site_worker` defaults to false. When true, a separate job deploys the
site Worker before staging, applying the `route` and `mode` inputs. The `image`
and `tag` inputs select an existing Cloudflare image or a GHCR `-all` tag (default `latest`).

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
example. Compose and the hosted container use the same database image.
The all-in-one server mounts `internal/mcpserver` over its canonical database.
Cloudflare `/mcp` and badge routes stay on the existing Worker until issue #51.

## Images

All names use the prefix `ghcr.io/taktekhq/lebanese-legislation`.

| Image | Holds | Used by |
| --- | --- | --- |
| `-db` | PostgreSQL 18.6, pgvector, both databases baked from `data/`, chunk text and hashes without embeddings, optional scans | Compose, database users and the base of `-all` |
| `-api` | Standalone API server, unchanged Dockerfile and amd64/arm64 support | Compose and API users with a separate database |
| `-mcp` | Standalone MCP server, unchanged Dockerfile and amd64/arm64 support | Compose and MCP clients with a separate database |
| `-all` | `-db` plus the Go server for dynamic pages, `/api/v1/`, `/mcp` and badges | Cloudflare Containers and self-hosted sites |
| `-vectors` | `vectorize-sync`, `export-vectors`, DuckDB 1.5.6; downloads Hugging Face vectors at runtime | The vectors workflow and users loading Vectorize or pgvector |
| `-site` | R2 `lebleg-site` static pages, assets, `404.html`, redirect data and a stdlib Go server | Static mirrors without a database |

Only `.github/workflows/images.yml` publishes these images. Each has its own
job; `-all` waits for `-db` when both are selected. All receive UTC date and
`latest` tags; `-all` also receives the git short SHA. Dispatch `which: all` or
a full image name. Pushes to main select images by their input paths. Successful
`static-site` and `vectors` runs refresh `-site` and `-vectors`, respectively.
Publishing of `-web` and `-data` has stopped; existing registry packages remain.
Compose builds the website service from `web/Dockerfile` locally.

The site image syncs every R2 key except `_build/` and `_export/`, retaining
`_export/redirects.json` privately for redirects. Its server uses the Worker's
escaped path mapping and security headers, and serves `404.html` for misses.
It listens on port 8080. It does not answer dynamic routes. Put a reverse proxy
in front and route `/api/`, `/mcp`, query-bearing requests such as `/search/?q=`,
and the other origin prefixes in `workers/static-site/src/route.js` to `-all:8080`.
API and MCP candidates are smoke-tested on both architectures before promotion;
existing immutable release versions are retained and new versions are added when free.
Tracking-only queries can stay on `-site`. No proxy or database credentials are
embedded in the static image. The runner frees disk before syncing the roughly
14 GB bucket; no local image build is required.
