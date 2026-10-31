#!/usr/bin/env bash
# Release checks for the four images on each platform, before version tags move.
# The services run against a throwaway PostgreSQL holding the website's
# fictional fixture (web/schema, web/testdata/synthetic.sql), never real data.
set -euo pipefail
: "${DB_IMAGE:?Set DB_IMAGE}"
: "${API_IMAGE:?Set API_IMAGE}"
: "${MCP_IMAGE:?Set MCP_IMAGE}"
: "${WEB_IMAGE:?Set WEB_IMAGE}"
root=$(cd "$(dirname "$0")/../.." && pwd)
net=lebleg-smoke-$$
pg=lebleg-smoke-pg-$$
services=()
cleanup() {
  docker rm -f "${services[@]}" "$pg" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker network create "$net" >/dev/null
docker run -d --name "$pg" --network "$net" -e POSTGRES_DB=legislation -e POSTGRES_HOST_AUTH_METHOD=trust \
  -v "$root/web/schema:/schema:ro" -v "$root/web/testdata:/testdata:ro" postgres:17-alpine >/dev/null
for _ in $(seq 1 60); do docker exec "$pg" pg_isready -U postgres -d legislation >/dev/null 2>&1 && break; sleep 1; done
for sql in /schema/schema.sql /schema/graph.sql /schema/amendments.sql /schema/official.sql /schema/indexes.sql /testdata/synthetic.sql; do
  docker exec "$pg" psql -q -v ON_ERROR_STOP=1 -U postgres -d legislation -f "$sql" >/dev/null
done
dsn="host=$pg user=postgres dbname=legislation sslmode=disable"

status() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
mcp() { curl -s -X POST -H 'Content-Type: application/json' -d "$2" "$1/mcp"; }
expect() { [ "$2" = "$3" ] || { echo "$platform: $1 returned $2, want $3" >&2; exit 1; }; }

for architecture in ${SMOKE_ARCHITECTURES:-amd64 arm64}; do
  platform="linux/$architecture"
  docker run --rm --platform "$platform" --entrypoint /opt/venv/bin/python "$DB_IMAGE" \
    -c 'import psycopg; from lebleg.restore import restore'
  docker run --rm --platform "$platform" --entrypoint bash "$DB_IMAGE" -n /opt/lebleg/db/web-schema.sh

  declare -A url=()
  for service in api mcp web; do
    image_var="${service^^}_IMAGE"
    port=8080; [ "$service" = api ] && port=8000
    name="lebleg-smoke-$service-$architecture-$$"
    services+=("$name")
    docker run -d --platform "$platform" --name "$name" --network "$net" -p "127.0.0.1::$port" \
      -e DATABASE_URL="$dsn" "${!image_var}" >/dev/null
    url[$service]="http://$(docker port "$name" "$port" | head -n 1)"
    for _ in $(seq 1 60); do docker exec "$name" /server healthcheck >/dev/null 2>&1 && break; sleep 1; done
    docker exec "$name" /server healthcheck >/dev/null || { docker logs "$name" >&2; exit 1; }
  done

  expect "api /api/v1/law/1/" "$(status "${url[api]}/api/v1/law/1/")" 200
  expect "api /mcp" "$(status "${url[api]}/mcp")" 404
  expect "api /" "$(status "${url[api]}/")" 404
  init=$(mcp "${url[mcp]}" '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}')
  grep -q '"serverInfo"' <<<"$init" || { echo "$platform: MCP initialize failed: $init" >&2; exit 1; }
  tools=$(mcp "${url[mcp]}" '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}')
  for tool in search_legislation get_law get_article get_ruling get_gazette_issue; do
    grep -q "\"$tool\"" <<<"$tools" || { echo "$platform: MCP is missing $tool" >&2; exit 1; }
  done
  expect "mcp /api/v1/" "$(status "${url[mcp]}/api/v1/")" 404
  expect "web /" "$(status -L "${url[web]}/")" 200
  expect "web /law/1/" "$(status -L "${url[web]}/law/1/")" 200

  docker rm -f "${services[@]}" >/dev/null
  services=()
  echo "$platform: db, api, mcp and web checks passed"
done
