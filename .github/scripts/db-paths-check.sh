#!/usr/bin/env bash
# Fails when a Go package that the db image's commands are built from is
# missing from either path list in .github/workflows/db.yml. Without it, a
# change to that package would skip the db image test.
set -euo pipefail
cd "$(dirname "$0")/../.."
workflow=.github/workflows/db.yml
module=$(go list -m)
missing=0
for pkg in $(go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' \
    ./cmd/build-search ./cmd/restore-release ./cmd/check-release ./cmd/build-release ./cmd/publish-release); do
  case "$pkg" in "$module"/*) ;; *) continue ;; esac
  dir=${pkg#"$module"/}
  if [ "$(grep -cF -e "- '$dir/**'" "$workflow")" -lt 2 ]; then
    echo "$workflow: add '$dir/**' to both path lists; the db image builds it" >&2
    missing=1
  fi
done
[ "$missing" -eq 0 ] && echo "db.yml path lists cover every package in the db image"
