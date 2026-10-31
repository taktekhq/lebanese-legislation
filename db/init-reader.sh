#!/bin/bash
set -euo pipefail
# Only fresh local clusters opt in. No provider key belongs in this service.
[ -n "${LEBLEG_READER_PASSWORD:-}" ] || exit 0
password64=$(printf '%s' "$LEBLEG_READER_PASSWORD" | base64 | tr -d '\n')
{
  printf 'CREATE ROLE lebleg_reader LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;\n'
  printf "SELECT format('ALTER ROLE lebleg_reader PASSWORD %%L',convert_from(decode('%s','base64'),'UTF8'))\\gexec\n" "$password64"
  printf "ALTER ROLE lebleg_reader SET default_transaction_read_only=on;\nALTER ROLE lebleg_reader SET statement_timeout='10s';\nALTER ROLE lebleg_reader CONNECTION LIMIT 16;\n"
} | psql -X -q -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB"
unset password64
