#!/usr/bin/env bash
set -euo pipefail
: "${DB_IMAGE:?Set DB_IMAGE}"
: "${API_IMAGE:?Set API_IMAGE}"
: "${MCP_IMAGE:?Set MCP_IMAGE}"
for architecture in amd64 arm64; do
  platform="linux/$architecture"
  docker run --rm --platform "$platform" --entrypoint /opt/venv/bin/python "$DB_IMAGE" \
    -c 'import psycopg; from lebleg_starter.restore import restore'
  for image in "$API_IMAGE" "$MCP_IMAGE"; do
    docker run --rm --platform "$platform" "$image" --help >/dev/null
    expected_mode=api
    if [ "$image" = "$MCP_IMAGE" ]; then expected_mode=mcp; fi
    docker run --rm --platform "$platform" --entrypoint python "$image" \
      -c 'import os,sys; from lebleg_starter.server import create_app; expected=sys.argv[1]; assert os.environ["APP_MODE"]==expected; paths={route.path for route in create_app().routes}; assert "/mcp" in paths; assert ("/api/v1/" in paths)==(expected=="api")' "$expected_mode"
  done
  echo "$platform: database restore, API and MCP modes passed"
done
