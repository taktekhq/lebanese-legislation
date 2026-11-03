# Static export of lebaneselegislation.com

The site is served by Cloudflare. About 760,000 static pages are rendered from
`data/` by [`cmd/static-export`](../cmd/static-export/README.md), using the
website's own handlers and templates.

The [static-site workflow](../.github/workflows/static-site.yml) runs 24 parallel
shards and uploads the rendered files to the R2 bucket `lebleg-site`. The
JavaScript Worker in [`workers/static-site`](../workers/static-site/) serves
them. The same Worker serves ruling PDFs from R2.

Search pages, `/api/v1/`, filters and Gazette scans go to one Cloudflare
Container. The [origin workflow](../.github/workflows/origin.yml) deploys it
from [`workers/static-site/container/Dockerfile`](../workers/static-site/container/Dockerfile),
with PostgreSQL 18.6, a baked-in database and the Go website server.

Semantic vectors live in Cloudflare Vectorize index `lebleg-chunks` and are
published as `vectors/vectors.parquet` on Hugging Face by the
[vectors workflow](../.github/workflows/vectors.yml).

See [architecture](architecture.md) for the full request and build paths,
and the [command README](../cmd/static-export/README.md) for local export,
comparison and upload instructions. This replaces the pre-deployment spike
notes from 1 November 2026.

## Render the static site yourself

Run the published database image, then export from a checkout of this repository:

```sh
docker run -d --name lebleg-db -e POSTGRES_PASSWORD=local \
  -p 127.0.0.1:5432:5432 ghcr.io/taktekhq/lebanese-legislation-db:latest
# Wait until the database is healthy before exporting.
export LEBLEG_DSN='postgresql://postgres:local@127.0.0.1:5432/lebleg'
go run ./cmd/static-export export -out site -workers 4 \
  -base https://example.org -search-shell
```

A full export takes about 3 to 4 hours on a 4-core machine: about 760,000 pages
at about 60 pages/s. Any web server can serve the files in `site/`. Configure its
404 response to use `404.html`. The exporter also writes `.export/redirects.json`:
the Cloudflare Worker uses this data at `_export/redirects.json` to resolve old
addresses. A mirror can implement the same redirects in its web server. Search
with `-search-shell` needs an API, configured through `LEBLEG_API_BASE` before export.

## Changed-page renders in CI

CI still loads PostgreSQL directly from the checkout's `data/` using `data/load.sh`.
It does not pull the `-db` image or wait for that image to build. The example above
is an independent option for people rendering locally.

The workflow reads `_export/rendered-commit` from R2 and pins the current checkout
for every job. `static-export changes -since COMMIT -out plan` reads the added and
removed COPY-text rows from `git diff data/tables`. It maps both old and new
memberships to law, decree, text, article, ruling, Gazette and document paths,
including their English twins. Graph edges, references, amendments, Gazette
entries and agreements identify linked pages. Related laws in the same year and
address kind, affected year lists, topic and code hubs, feeds, home, changes and
sitemaps are refreshed too. List pagination is refreshed as a group when its
membership or labels change, so insertions and removals cannot leave stale pages.

Each shard exports the planned list with `-paths`, using the existing `Owner`
function. This does not crawl the site. It discovers only new parts of selected
long laws, and includes old parts and list pagination from its manifest to remove
obsolete files. Unvisited manifest entries are retained. Removed pages and pages
that become redirects are deleted from R2 by their owner. Full renders retain the
existing R2 path exchange for crawl discovery.

A missing baseline, `workflow_dispatch` with `full=true`, or a renderer change
(`internal/site`, templates, assets, `cmd/static-export`, `internal/staticexport`)
forces a full render. Schema, rendering dependencies and unmapped table changes
also fall back to a full render. The workflow serializes runs and keeps 24 owners,
matching the stored manifests. Only after all shards and the generation job
succeed does the final job write `_export/rendered-commit`. A failed run leaves
the baseline unchanged, so its changes are retried.
