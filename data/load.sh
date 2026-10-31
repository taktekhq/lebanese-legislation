#!/bin/sh
# Build the Lebanese Legislation database from the text in this repository.
#
#   createdb lebleg && ./load.sh "dbname=lebleg"
#
# Needs only psql (PostgreSQL 16 or later) and an empty database. Each table
# loads as a single COPY, parents before children (see TABLES), then the
# indexes are built.
set -eu
cd "$(dirname "$0")"
DB="${1:-}"
run() { psql ${DB:+"$DB"} -X -v ON_ERROR_STOP=1 -q "$@"; }

run -f schema.sql
while read -r t; do
  cols=$(cat "tables/$t/COLUMNS")
  if ls "tables/$t"/*.tsv >/dev/null 2>&1; then
    cat "tables/$t"/*.tsv | run -c "\\copy $t ($cols) FROM STDIN"
  fi
  echo "loaded $t"
done < TABLES
run -f indexes.sql
run -c "ANALYZE"
echo "done"
