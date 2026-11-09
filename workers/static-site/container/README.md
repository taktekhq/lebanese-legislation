# Origin images and deployment

`db/Dockerfile` builds both databases from `data/` with the existing chunker,
without embedding calls or database dumps. The `images` workflow
publishes `-db:<YYYY-MM-DD>` and `:latest` on demand or on
changes to `data/` on main.

`db/prod/Dockerfile` builds `-db-prod` from `-db`: the same database plus
the read-optimized tables of `db/prod.sql` (`law_card`, `article_card`, and
`possibly_affected` stored as a table). It adds no data; every extra table is
computed from `-db`, and the replies are byte for byte the same. The `data`
workflow builds it after `-db` and before `-all`. `-db` stays the public
dataset image that strangers pull; the MCP server also runs on it, computing
the same columns at request time (slower under parallel calls).

`Dockerfile` builds the Go server and adds it and `start.sh` to `-db-prod` at
`DATA_TAG`. Build from the repository root with
`docker build -f workers/static-site/container/Dockerfile --build-arg DATA_TAG=latest .`.
The server serves dynamic pages, `/api/v1/`, and the ten-tool
`internal/mcpserver` handler at `/mcp` over the same canonical database.
The site Worker routes MCP, badge and old `/scan/` requests to the live origin container.
Old scan file URLs redirect to the original PDF on SaderLex; scan files are not hosted.

The `origin` workflow takes `tag` (default `latest`) and pulls the published
GHCR `-all` image, then copies it to the Cloudflare registry with a unique
commit, run and attempt tag. An already pushed Cloudflare registry `image`
skips that copy. Origin does not build images. Runs are serialized.

The site Worker has service bindings to `lebleg-origin-blue` and
`lebleg-origin-green`, the two environments of `workers/origin`. Each owns one
`Origin` container instance named `main`. Neither origin Worker has public routes
or a workers.dev address. Deploying an origin Worker only restarts its own side.

The workflow runs these jobs:

1. `image` copies the published all-in-one image to Cloudflare, unless `image`
   was supplied.
2. `site-worker` optionally deploys `workers/static-site` when
   `deploy_site_worker` is true (default false). Only this job applies the `route`
   input and sets `ORIGIN_TEST_KEY` on the site Worker.
3. `stage` reads R2 `_build/live-side` (default blue), deploys only the idle
   environment with the new image, and sets its `GEMINI_API_KEY` and
   `LEBLEG_VECTORIZE_TOKEN` secrets. The live environment is never deployed.
4. `warm` checks the idle side through the site Worker for up to 30 minutes.
   Probes keep the named `lebleg-origin-deploy/1` User-Agent.
5. `switch` writes the idle name to `_build/live-side` and waits 20 seconds for
   the site Worker's 15 second memory cache to expire. It does not deploy.
6. `verify` checks the public site for up to 60 seconds. Failure writes the old
   side back to the flag and waits 20 seconds. Success retires the old container
   through `GET /_origin/restart?side=<old>` with the test header.

`origin-state` is a short-lived workflow artifact recording the old and idle side
and the new image. Switch and rollback only need the side names.
All dynamic requests use the live blue/green container.

The workflow requires `CLOUDFLARE_WORKERS_TOKEN`, `R2_ACCESS_KEY_ID`,
`R2_SECRET_ACCESS_KEY`, `ORIGIN_TEST_KEY` and `LEBLEG_GEMINI_API_KEY`.
The Vectorize token uses `CLOUDFLARE_WORKERS_TOKEN`. Origin secrets are set per
environment. The database and origin workflows use `GITHUB_TOKEN` with packages write
permission to publish their GHCR images.

## First production migration

Before deploying the new site Worker, deploy both origin environments with the
current production image and set both secrets on each. Warm each `main` instance
and pass all three checks through a temporary local service-binding probe Worker
(`wrangler dev --remote` with both bindings), since the old site Worker still
addresses its own containers. Stop the probe after checking. Write `blue` to R2
`_build/live-side`, then deploy the new site Worker with the existing production
routes, the intended mode and `ORIGIN_TEST_KEY`. This deployment deletes the old
container classes, so both replacement Workers must already exist and the selected
side must be warm. Verify public traffic before running routine idle-side deploys.
The probe must forward to the same `main` instance used by each origin Worker.

Run local checks from the repository root:

```sh
node --test workers/static-site/test/*.test.mjs
node --test workers/origin/test/*.test.mjs
python3 -m unittest discover -s workers/static-site/test -p 'origin_deploy_test.py'
go build ./...
```

An authenticated `x-origin-test` request can choose `x-origin-side: blue|green`.
These requests bypass static pages and API caches. The log and restart endpoints
use `?side=blue|green` with the same secret header. Normal visitors follow the R2
flag. API edge cache keys include the selected side.

## Images

All names use the prefix `ghcr.io/taktekhq/lebanese-legislation`.

| Image | Holds | Used by |
| --- | --- | --- |
| `-db` | PostgreSQL 18.6, pgvector, both databases baked from `data/`, chunk text and hashes without embeddings | Compose, database users and the base of `-all` |
| `-api` | Standalone API server, unchanged Dockerfile and amd64/arm64 support | Compose and API users with a separate database |
| `-mcp` | Standalone MCP server, unchanged Dockerfile and amd64/arm64 support | Compose and MCP clients with a separate database |
| `-all` | `-db` plus the Go server for dynamic pages, `/api/v1/`, `/mcp` and badges | Compose, Cloudflare Containers and self-hosted sites |
| `-vectors` | `vectorize-sync`, `export-vectors`, DuckDB 1.5.6; downloads Hugging Face vectors at runtime | The vectors workflow and users loading Vectorize or pgvector |

Only `.github/workflows/images.yml` publishes these images. Each has its own
job; `-all` waits for `-db` when both are selected. All receive UTC date and
`latest` tags; `-all` also receives the git short SHA. Dispatch `which: all` or
a full image name. Pushes to main select images by their input paths. Successful
`vectors` runs refresh `-vectors`. Static pages are published directly to R2.
Publishing of `-web` and `-data` has stopped; existing registry packages remain.
Compose pulls `ghcr.io/taktekhq/lebanese-legislation-all:latest` for the website, including its database, API and MCP.

API and MCP candidates are smoke-tested on both architectures before promotion;
existing immutable release versions are retained and new versions are added when free.
