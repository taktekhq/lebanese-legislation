# Static export of lebaneselegislation.com

The site is served by Cloudflare. About 760,000 static pages are rendered from
`data/` by [`cmd/static-export`](../cmd/static-export/README.md), using the
website's own handlers and templates.

The [static-site workflow](../.github/workflows/static-site.yml) runs 8 parallel
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
