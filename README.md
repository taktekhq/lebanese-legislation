# Lebanese Legislation

The Go website, search, JSON API and MCP server behind [Lebanese Legislation](https://lebaneselegislation.com). It reads an existing PostgreSQL database. Arabic and English interfaces share the same source texts and citations.

## Run

Use Go 1.27.1 or newer. Point `DATABASE_URL` at a compatible database, then start the server:

```sh
export DATABASE_URL='postgres://USER:PASSWORD@HOST/DATABASE?sslmode=require'
go run ./cmd/website
```

Open http://localhost:8080. `LISTEN_ADDR` defaults to `127.0.0.1:8080`. Set `PUBLIC_URL` to your public origin for canonical links. Use the existing database release and database image from [lebanese-legislation-starter](https://github.com/taktekhq/lebanese-legislation-starter) for the published dataset. This repository does not rebuild that distribution.

## Synthetic demo

Docker Compose starts stock PostgreSQL with one fictional law and article:

```sh
docker compose -f compose.demo.yml up -d --wait
export DATABASE_URL='postgres://postgres@localhost:55439/legislation?sslmode=disable'
go run ./cmd/website
```

Search for `المكتبة`, open `/law/1/`, query `/api/v1/search/?q=المكتبة`, or connect an MCP client to `/mcp`. The fixture is not legal material. Remove it with `docker compose -f compose.demo.yml down -v`.

## Check

```sh
go test ./...
DEMO_DATABASE_URL="$DATABASE_URL" go test ./internal/site -run TestSyntheticServing
```

The smoke test requires the synthetic demo. Additional corpus checks opt in through `LEBLEG_DSN`. Optional provider-backed meaning search is disabled. Scan files are optional local assets. Bundled fonts are omitted; browsers use installed fallbacks.

MIT licensed code. Source documents retain their own rights and attribution.

## Containers

Build either runtime from this source:

```sh
docker build --target website -t lebanese-legislation .
docker build --target mcp -t lebanese-legislation-mcp .
docker run --rm -p 127.0.0.1:8080:8080 --env DATABASE_URL lebanese-legislation-mcp
```

Use a read-only database role. MCP mode exposes `/mcp`, `/mcp/` and `/healthz`. The website image also serves search, HTML and JSON. Both images run without root, contain no data or credentials, and accept `DATABASE_URL`, `PUBLIC_URL` and `LISTEN_ADDR` at runtime. Published images are built by [the image workflow](.github/workflows/images.yml).

For Cloudflare, use the [Containers example](cloudflare/README.md). To help with search, code or transcriptions, see [CONTRIBUTING.md](CONTRIBUTING.md).

Try the [handwriting demo](https://huggingface.co/spaces/taktekhq/lebanese-legal-handwriting), or run its [source](examples/handwriting/README.md) locally. It downloads drafts for review; it does not submit annotations.
