#!/bin/bash
# Adds what the website in web/ reads to a restored corpus (db/web.sql). Runs
# once after the first restore. On a database restored by 0.2.0 or older, run:
#   docker compose exec db bash /opt/lebleg/db/web-schema.sh
set -euo pipefail
user=${POSTGRES_USER:-postgres}
db=(psql -X -q -v ON_ERROR_STOP=1 --username "$user" --dbname "${POSTGRES_DB:-$user}")
if [ "$("${db[@]}" -tAc "SELECT to_regclass('public.legislation_article') IS NOT NULL")" != t ]; then
  echo "web-schema: no corpus tables, nothing to add"
  exit 0
fi
"${db[@]}" -f "$(dirname "$0")/web.sql"
echo "web-schema: website functions, columns, tables and indexes are in place"
