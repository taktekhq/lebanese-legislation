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
# The unofficial English of popular texts' articles (article_en.jsonl, made by
# cmd/translate-en), kept where the Arabic it was made from is still the text.
if [ -f article_en.jsonl ]; then
  run -f article_en.sql
  echo "loaded article_en"
fi
run -c "ANALYZE"
echo "done"
