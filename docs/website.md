# Build the website

The Go website provides Arabic and English pages. Use Go 1.27.1 or newer and a compatible PostgreSQL database:

```sh
export DATABASE_URL='postgres://USER:PASSWORD@HOST/DATABASE?sslmode=require'
go run ./cmd/website
```

Run these commands from the repository root. Open http://localhost:8080. `LISTEN_ADDR` defaults to `127.0.0.1:8080`; `PUBLIC_URL` sets canonical links. This standalone website retains its SQL browsing and keyword search. Provider-backed meaning search is disabled here; the shared API/MCP service supports semantic retrieval.

To build a local image:

```sh
docker build --target website -t lebanese-legislation-website .
```

The website image is source-build only.

## Fictional fixture

```sh
docker compose -f compose.demo.yml up -d --wait
export DATABASE_URL='postgres://postgres@localhost:55439/legislation?sslmode=disable'
go run ./cmd/website
```

Search for `المكتبة`, open `/law/1/`, or query `/api/v1/search/?q=المكتبة`. Stop with `docker compose -f compose.demo.yml down -v`.

```sh
go test ./...
DEMO_DATABASE_URL="$DATABASE_URL" go test ./internal/site -run TestSyntheticServing
```
