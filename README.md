# Lebanese Legislation website

The Go website behind [Lebanese Legislation](https://lebaneselegislation.com), with Arabic and English pages. Build it from this repository for local or offline hosting.

For the public DB, general/search API and MCP images, use the [service repository](https://github.com/taktekhq/lebanese-legislation-starter). API and MCP share its retrieval backend. This repository does not rebuild the database distribution.

## Run the website

Use Go 1.27.1 or newer and a compatible PostgreSQL database:

```sh
export DATABASE_URL='postgres://USER:PASSWORD@HOST/DATABASE?sslmode=require'
go run ./cmd/website
```

Open http://localhost:8080. `LISTEN_ADDR` defaults to `127.0.0.1:8080`; `PUBLIC_URL` sets canonical links. The source website keeps its existing SQL browsing and keyword-search behavior. Optional provider-backed meaning search is disabled in this standalone build.

Build an image locally when useful:

```sh
docker build --target website -t lebanese-legislation-website .
```

The website image is not published. Earlier published tags remain available.

## Synthetic demo

```sh
docker compose -f compose.demo.yml up -d --wait
export DATABASE_URL='postgres://postgres@localhost:55439/legislation?sslmode=disable'
go run ./cmd/website
```

The fixture contains one fictional law and article. Search for `المكتبة`, open `/law/1/`, or query `/api/v1/search/?q=المكتبة`. Stop with `docker compose -f compose.demo.yml down -v`.

```sh
go test ./...
DEMO_DATABASE_URL="$DATABASE_URL" go test ./internal/site -run TestSyntheticServing
```

## Services and contributions

Canonical images are `ghcr.io/taktekhq/lebanese-legislation-db:0.1.5`, `ghcr.io/taktekhq/lebanese-legislation-api:0.1.5` and `ghcr.io/taktekhq/lebanese-legislation-mcp:0.1.5`. The MCP target here wraps the matching API image and limits it to MCP/health routes. [Cloudflare Containers](cloudflare/README.md) can run it.

Try the [handwriting demo](https://huggingface.co/spaces/taktekhq/lebanese-legal-handwriting), or run its [source](examples/handwriting/README.md). Drafts stay local and download for review. See [CONTRIBUTING.md](CONTRIBUTING.md) for code and search reports.

MIT licensed code. Source documents retain their own rights and attribution. Scan files are optional local assets; system font fallbacks are used.

The MCP image follows verified API releases automatically. A wrapper is built only when the API version advances; dataset updates reuse the existing images.
