#!/bin/bash
set -euo pipefail
# Socket-only init PostgreSQL must never satisfy readiness.
pg_isready -h 127.0.0.1 -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-postgres}" >/dev/null
/opt/venv/bin/python /opt/lebleg/db/release.py health
# A restored corpus also needs the website's columns and tables (db/web.sql).
user=${POSTGRES_USER:-postgres}
ok=$(psql -X -tA --username "$user" --dbname "${POSTGRES_DB:-$user}" \
  -c "SELECT to_regclass('public.legislation_article') IS NULL OR to_regclass('public.web_schema') IS NOT NULL")
if [ "$ok" != t ]; then
  echo "website schema missing: run docker compose exec db bash /opt/lebleg/db/web-schema.sh" >&2
  exit 1
fi
