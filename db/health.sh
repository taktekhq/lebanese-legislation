#!/bin/bash
set -euo pipefail
# Socket-only init PostgreSQL must never satisfy readiness.
user=${POSTGRES_USER:-postgres}
pg_isready -h 127.0.0.1 -U "$user" -d "${POSTGRES_DB:-postgres}" >/dev/null
if [ -z "${POSTGRES_PASSWORD:-}" ] && [ -n "${POSTGRES_PASSWORD_FILE:-}" ]; then
  POSTGRES_PASSWORD=$(cat "$POSTGRES_PASSWORD_FILE")
fi
# One verified release receipt; with LEBLEG_MANIFEST_FILE, that very release.
PGPASSWORD="${POSTGRES_PASSWORD:-}" check-release -db "host=/var/run/postgresql dbname=${POSTGRES_DB:-$user} user=$user connect_timeout=5"
# A restored corpus also needs the website's columns and tables (web.sql).
ok=$(psql -X -tA --username "$user" --dbname "${POSTGRES_DB:-$user}" \
  -c "SELECT to_regclass('public.legislation_article') IS NULL OR to_regclass('public.web_schema') IS NOT NULL")
if [ "$ok" != t ]; then
  echo "website schema missing: run docker compose exec db bash /opt/lebleg/db/web-schema.sh" >&2
  exit 1
fi
