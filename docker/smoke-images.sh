#!/usr/bin/env bash
set -euo pipefail
: "${DB_IMAGE:?Set DB_IMAGE}"
: "${API_IMAGE:?Set API_IMAGE}"
for architecture in amd64 arm64; do
  platform="linux/$architecture"
  docker run --rm --platform "$platform" --entrypoint /opt/venv/bin/python "$DB_IMAGE" \
    -c 'import psycopg; from lebleg_starter.restore import restore'
  for image in "$API_IMAGE"; do
    docker run --rm --platform "$platform" "$image" --help >/dev/null
    docker run --rm --platform "$platform" --entrypoint python "$image" \
      -c 'from lebleg_starter.server import create_app; create_app()'
  done
  echo "$platform: database restore and shared API backend imports passed"
done
