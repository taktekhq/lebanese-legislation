# Website

The Go website behind [LebaneseLegislation.com](https://lebaneselegislation.com/), with Arabic and English pages and search. It is published as `ghcr.io/taktekhq/lebanese-legislation-web`, and `./start` at the repository root runs it on port 8080 against the public database. The code is shared with the [API](../api/) and the [MCP server](../mcp/) in [`internal/`](../internal/).

The live site runs a newer private build. This copy was exported on 30 October 2026.

Run it from the repository root with Go 1.27.1 or newer:

```sh
export DATABASE_URL='postgres://USER:PASSWORD@HOST/DATABASE?sslmode=require'
go run ./web
```

Open http://localhost:8080. `LISTEN_ADDR` defaults to `127.0.0.1:8080`. `PUBLIC_URL` sets absolute links. `GEMINI_API_KEY` turns on search by meaning.

The database needs the tables in [`schema/`](schema/). A restore of the public dump adds what the dump lacks ([`web.sql`](../tools/src/lebleg/sql/web.sql)). Four tables stay empty there because their data is not public: Constitutional Council rulings, noindex marks, page change dates and official Gazette texts.

Build the image from the repository root:

```sh
docker build -f web/Dockerfile -t lebanese-legislation-web .
```

## Try it on fictional data

```sh
docker compose -f web/compose.demo.yaml up -d --wait
export DATABASE_URL='postgres://postgres@localhost:55439/legislation?sslmode=disable'
go run ./web
```

Search for `المكتبة`, open `/law/1/`, or query `/api/v1/search/?q=المكتبة`. Stop with `docker compose -f web/compose.demo.yaml down -v`.

```sh
go test ./...
DEMO_DATABASE_URL="$DATABASE_URL" go test ./internal/site -run TestSyntheticServing
```
