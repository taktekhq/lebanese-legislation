# Architecture

Current setup after the changes of 1 to 4 November 2026. The former private
`taktekhq/lebaneselegislation` repository is folded into this public repository:
code, scraper, build pipeline, website and MCP.

## Request path

A visitor requests lebaneselegislation.com through Cloudflare. The JavaScript
Worker in [`workers/static-site`](../workers/static-site/) serves a static page
from the R2 bucket `lebleg-site`, or sends a dynamic request to one Cloudflare
Container. Ruling PDFs are also served from R2 by the same Worker.

Search pages, `/api/v1/`, `/mcp`, badges, filters and old `/scan/` links go to
the container. A `/scan/` link answers with a 301 to the original Gazette PDF on
SaderLex; Gazette page images are no longer hosted.
Its [Dockerfile](../workers/static-site/container/Dockerfile) packages
PostgreSQL 18.6 with the database baked into the image and the Go website server.
Semantic search uses Cloudflare Vectorize index `lebleg-chunks`: 1,547,937 chunks,
768 dimensions, model `gemini-embedding-001`. The vectors have moved out of
PostgreSQL into Vectorize.

## Build path

`data/` is the input for the site build. GitHub Actions renders about 760,000
static pages through `cmd/static-export` in 24 parallel shards and uploads them
to `lebleg-site`. Each run renders only what changed: the paths whose table
rows changed, the pages that used a changed template, UI string or topic guide
(from each render's `deps.json`), or everything after a Go change. A plan of up
to 5,000 paths renders on one runner. A header or footer change uploads only the
shared layout frames. Every render starts its database from the `-db` image
tagged for that commit's data (`data-<hash>`), or loads `data/` when no such image
exists, and sends its changed pages to IndexNow. Pushes to main that touch the
site start a deploy; `static-site-catchup.yml` runs after each deploy and every
two hours, and reruns a failed one. See [static export](static-export.md) for the
R2 commit baseline, dependency mapping and local rendering instructions.
Image-producing workflows publish their own images; origin deploys `-all`.
The vectors workflow filters the public vectors to IDs generated from current `data/`,
publishes Parquet on Hugging Face and loads Vectorize through the vectors image.
Later syncs upload changed vectors and delete IDs absent from the current set.
`cmd/embed-missing` embeds chunks that have no vector yet with Gemini, at most
$1 and 20,000 chunks a day; a run stopped by the budget resumes from the Actions
cache. Any chunk still missing a vector stops the workflow before publication or sync. The 21 stale
archive IDs found in the comparison are included in the deletion baseline.

| Workflow | Output |
| --- | --- |
| [static-site.yml](../.github/workflows/static-site.yml) | `data/` to static pages in R2, using `cmd/static-export` and 24 shards (one runner for small plans), then IndexNow. |
| [static-site-catchup.yml](../.github/workflows/static-site-catchup.yml) | After each deploy and every two hours: starts static-site when the live site is behind main, reruns a failed deploy once. |
| [images.yml](../.github/workflows/images.yml) | Manual button to publish one selected image through `build-image.yml`. |
| [origin.yml](../.github/workflows/origin.yml) | Container image with the database and Go website server, deployed to Cloudflare Containers. |
| [vectors.yml](../.github/workflows/vectors.yml) | `build` then `vectorize` and `publish`: current chunk IDs filter the public Parquet, Hugging Face receives `vectors/vectors.parquet`, and the vectors image syncs `lebleg-chunks`. |
| [data.yml](../.github/workflows/data.yml) | `data/` to the source-table Parquet files on Hugging Face, then the `-db`, `-db-prod` and `-all` images. A `db/prod`-only push builds just `-db-prod` and `-all`. |
| [hf-card.yml](../.github/workflows/hf-card.yml) | The dataset card, `METHODS.md` and `SCHEMA.md` from `.github/huggingface/` to Hugging Face. |

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
provides source tables as Parquet and the openly published
[vectors Parquet](https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet).
The vectors file has columns `id`, `document`, `model`, `hash` and `values`
(`FLOAT[768]`). See the [service reference](reference.md#vectors) for a DuckDB
example. Compose and the hosted container use the same database image.
The all-in-one server mounts `internal/mcpserver` over its canonical database.
The site Worker sends `/mcp` and the badge routes to that container.

## Images

All names use the prefix `ghcr.io/taktekhq/lebanese-legislation`.

| Image | Holds | Used by |
| --- | --- | --- |
| `-db` | PostgreSQL 18.6, pgvector, both databases baked from `data/`, chunk text and hashes without embeddings | Compose, database users, the static render and the base of `-all` |
| `-api` | Standalone API server, unchanged Dockerfile and amd64/arm64 support | Compose and API users with a separate database |
| `-mcp` | Standalone MCP server, unchanged Dockerfile and amd64/arm64 support | Compose and MCP clients with a separate database |
| `-all` | `-db` plus the Go server for dynamic pages, `/api/v1/`, `/mcp` and badges | Compose, Cloudflare Containers and self-hosted sites |
| `-vectors` | `vectorize-sync`, `export-vectors`, DuckDB 1.5.6; downloads Hugging Face vectors at runtime | The vectors workflow and users loading Vectorize or pgvector |

Image builds use the reusable `build-image.yml` workflow. Each image has its own
`images-<name>` job concurrency group across callers; pushes cancel an older
build of that image. Producer jobs have separate locks, so a slow image never
holds a workflow-wide lock.

| Image | Built in workflow | When |
| --- | --- | --- |
| `-db` | `data.yml` | After `publish` succeeds on main. Also tagged `data-<hash>` (`.github/scripts/render-db-tag.sh`) for the static render. |
| `-db-prod` | `data.yml` | After `-db`; on a push that changes only `db/prod.sql` or `db/prod/`, alone, from the published `-db:data-<hash>`. |
| `-api` | `checks.yml` (`checks`) | After `ci-ok` succeeds on a push to main. |
| `-mcp` | `checks.yml` (`checks`) | After `ci-ok` succeeds on a push to main. |
| `-all` | `data.yml`, `checks.yml` | After `-db-prod` succeeds, or after `ci-ok` on a push to main. Always pulls `-db-prod:latest`. |
| `-vectors` | `vectors.yml` | After `publish` succeeds on main. |

`images.yml` has only a manual dispatch with an `image` suffix choice; it builds
one selected image. Images receive UTC date and `latest` tags; `-all` also gets
the git short SHA. API and MCP retain candidate checks and release promotion.
Successful main runs of `checks` and `data` automatically start origin's existing
blue/green deployment only when their `all / image-all` job succeeded. Manual
origin dispatch still accepts an image or tag, defaulting to `-all:latest`.
Publishing of `-web` and `-data` has stopped; existing registry packages remain.
Compose pulls `ghcr.io/taktekhq/lebanese-legislation-all:latest` for the website, including its database, API and MCP.

API and MCP candidates are smoke-tested on both architectures before promotion;
existing immutable release versions are retained and new versions are added when free.
