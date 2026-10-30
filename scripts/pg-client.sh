#!/bin/sh
# Link this file as pg_dump and pg_restore when matching clients are not installed.
set -eu
client=$(basename "$0")
case "$client" in pg_dump|pg_restore) ;; *) echo 'Use a pg_dump or pg_restore link' >&2; exit 2 ;; esac
image=${PG_CLIENT_IMAGE:-ghcr.io/taktekhq/lebanese-legislation-db:0.1.5}
mount=${PG_CLIENT_MOUNT:-/tmp}
exec docker run --rm -i --network host --read-only --cap-drop ALL \
  --user "$(id -u):$(id -g)" --volume "$mount:$mount:ro" \
  --env PGHOST --env PGPORT --env PGDATABASE --env PGUSER --env PGPASSWORD \
  --env PGSSLMODE --env PGOPTIONS --entrypoint "$client" "$image" "$@"
