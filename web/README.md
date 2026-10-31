# Website

The Go website behind [LebaneseLegislation.com](https://lebaneselegislation.com/), with Arabic and English pages. It is here for local or offline hosting and is not published as an image. It has keyword search over SQL; semantic search lives in the [server](../server/).

The live site runs a newer private build. This copy was exported on 30 October 2026.

Run it with Go 1.27.1 or newer from this folder:

```sh
export DATABASE_URL='postgres://USER:PASSWORD@HOST/DATABASE?sslmode=require'
go run ./cmd/website
```

Open http://localhost:8080. `LISTEN_ADDR` defaults to `127.0.0.1:8080`. `PUBLIC_URL` sets canonical links.

The database needs the tables in [`schema/`](schema/). The public dump from the [db image](../db/) has them, except the `search_exact` columns: pages and law lists work against it, keyword search does not yet.

Build an image from the repository root:

```sh
docker build -f web/Dockerfile -t lebanese-legislation-web .
```

## Try it on fictional data

```sh
docker compose -f compose.demo.yaml up -d --wait
export DATABASE_URL='postgres://postgres@localhost:55439/legislation?sslmode=disable'
go run ./cmd/website
```

Search for `المكتبة`, open `/law/1/`, or query `/api/v1/search/?q=المكتبة`. Stop with `docker compose -f compose.demo.yaml down -v`.

```sh
go test ./...
DEMO_DATABASE_URL="$DATABASE_URL" go test ./internal/site -run TestSyntheticServing
```
