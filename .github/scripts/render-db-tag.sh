#!/bin/bash
# Prints the db image tag for commit $1 (default HEAD): data-<16 hex>, from
# git's hashes of every file the bake reads for the lebleg database: data/,
# db/ (bake.sh, official.sql, the Dockerfile with its pinned PostgreSQL) and
# the website SQL. Notes (*.md) never reach the database and are left out,
# and so does db/prod* (the -db-prod layer, built on top of -db).
# build-image.yml tags each db image with it, and a render uses the image only
# when the tag of its own commit is published, so it never reads other data.
set -euo pipefail
rev=${1:-HEAD}
h=$(git ls-tree -r "$rev" -- data db tools/src/lebleg/sql/web.sql | grep -v '\.md$' | grep -Ev '\sdb/prod(\.sql$|/)' | sha256sum | cut -c1-16)
echo "data-$h"
