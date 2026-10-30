#!/usr/bin/env bash
set -euo pipefail
: "${POSTGRES_IMAGE:?Set POSTGRES_IMAGE}"
: "${STARTER_IMAGE:?Set STARTER_IMAGE}"
: "${PUBLISHER_IMAGE:?Set PUBLISHER_IMAGE}"
container=""
cleanup() {
  if [[ -n "$container" ]]; then docker rm -fv "$container" >/dev/null; fi
}
trap cleanup EXIT
for architecture in amd64 arm64; do
  platform="linux/$architecture"
  container="lebleg-image-smoke-$architecture-$$"
  docker run -d --platform "$platform" --name "$container" \
    -e POSTGRES_HOST_AUTH_METHOD=trust "$POSTGRES_IMAGE" >/dev/null
  ready=false
  for attempt in $(seq 1 60); do
    if docker exec "$container" pg_isready -U postgres >/dev/null 2>&1; then
      ready=true
      break
    fi
    sleep 2
  done
  if [[ "$ready" != true ]]; then
    echo "PostgreSQL did not become ready on $platform" >&2
    exit 1
  fi
  result=$(docker exec "$container" psql -U postgres -At -v ON_ERROR_STOP=1 \
    -c "CREATE EXTENSION vector; SELECT '[1,2,3]'::vector <-> '[1,2,4]'::vector;")
  [[ "${result##*$'\n'}" == 1 ]]
  docker run --rm --platform "$platform" "$STARTER_IMAGE" --help >/dev/null
  docker run --rm --platform "$platform" --entrypoint /opt/venv/bin/python \
    "$PUBLISHER_IMAGE" -c 'import pyarrow, huggingface_hub'
  cleanup
  container=""
  echo "$platform: PostgreSQL, pgvector, restore CLI and publisher imports passed"
done
