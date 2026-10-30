#!/usr/bin/env bash
set -euo pipefail
: "${DB_IMAGE:?Set DB_IMAGE}"
: "${API_IMAGE:?Set API_IMAGE}"
for architecture in amd64 arm64; do
  platform="linux/$architecture"
  docker run --rm --platform "$platform" --entrypoint /opt/venv/bin/python "$DB_IMAGE" \
    -c 'import psycopg; from lebleg_starter.restore import restore'
  docker run --rm --platform "$platform" "$API_IMAGE" --help >/dev/null
  for expected_mode in api mcp; do
    docker run --rm --platform "$platform" --env "APP_MODE=$expected_mode" --entrypoint python "$API_IMAGE" \
      -c 'import os,sys; from lebleg_starter.server import create_app; expected=sys.argv[1]; assert os.environ["APP_MODE"]==expected; paths=set(create_app().openapi()["paths"]); assert "/mcp" in paths; assert ("/api/v1/" in paths)==(expected=="api")' "$expected_mode"
  done
  echo "$platform: database restore, API and MCP modes passed"
done
