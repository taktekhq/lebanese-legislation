#!/bin/bash
# Adds what the Go services read to a database restored by 0.2.0 or older
# (newer restores apply web.sql themselves). Run once:
#   docker compose exec db bash /opt/lebleg/db/web-schema.sh
set -euo pipefail
user=${POSTGRES_USER:-postgres}
db=(psql -X -q -v ON_ERROR_STOP=1 --username "$user" --dbname "${POSTGRES_DB:-$user}")
if [ "$("${db[@]}" -tAc "SELECT to_regclass('public.legislation_article') IS NOT NULL")" != t ]; then
  echo "web-schema: no corpus tables, nothing to add"
  exit 0
fi
"${db[@]}" --single-transaction -f "$(dirname "$0")/web.sql" \
  -c "DO \$\$ BEGIN IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'lebleg_reader') THEN GRANT SELECT ON ALL TABLES IN SCHEMA public TO lebleg_reader; END IF; END \$\$"
echo "web-schema: website functions, columns, tables and indexes are in place"
