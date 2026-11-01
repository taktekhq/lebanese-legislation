#!/bin/bash
set -euo pipefail
# Runs once, after init-reader.sh, against the official image's socket-only
# init server: downloads, verifies and restores the release (restore-release).
user=${POSTGRES_USER:-postgres}
if [ -z "${POSTGRES_PASSWORD:-}" ] && [ -n "${POSTGRES_PASSWORD_FILE:-}" ]; then
  POSTGRES_PASSWORD=$(cat "$POSTGRES_PASSWORD_FILE")
fi
# The password goes through the environment, never a command line.
export PGPASSWORD="${POSTGRES_PASSWORD:-}"
args=(-db "host=/var/run/postgresql dbname=${POSTGRES_DB:-$user} user=$user connect_timeout=5"
  -web-sql /opt/lebleg/db/web.sql)
# The reader role exists only when init-reader.sh created it.
if [ -n "${LEBLEG_READER_PASSWORD:-}" ]; then args+=(-reader-role lebleg_reader); else args+=(-reader-role ""); fi
if [ -n "${LEBLEG_MANIFEST_FILE:-}" ]; then
  args+=(-manifest "$LEBLEG_MANIFEST_FILE")
  if [ -n "${LEBLEG_DUMP_FILE:-}" ]; then args+=(-dump "$LEBLEG_DUMP_FILE"); fi
else
  args+=(-repo "${DATASET_REPO:-taktekhq/lebanese-legislation}" -revision "${DATASET_REVISION:-main}"
    -manifest-path "${MANIFEST_PATH:-releases/latest.json}" -cache-dir "${LEBLEG_CACHE_DIR:-$PGDATA/release-cache}")
fi
restore-release "${args[@]}"
