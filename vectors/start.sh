#!/bin/sh
set -eu
# A mounted input skips the download; /work can persist the sync manifest.
input=${VECTORS_INPUT:-/work/vectors.parquet}
if [ -z "${VECTORS_INPUT:-}" ]; then
  curl -fL --retry 3 -o "$input.tmp" "${VECTORS_URL:-https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet}"
  mv "$input.tmp" "$input"
fi
exec vectorize-sync -in "$input" -prev "${VECTORS_PREVIOUS:-/work/vectors-manifest.tsv}" -manifest "${VECTORS_MANIFEST:-/work/vectors-manifest.tsv}" "$@"
