#!/usr/bin/env bash
# Check exported Parquet tables against data/stats.json, and optionally against
# another directory of the same tables (the dataset on Hugging Face).
#
#   .github/scripts/data-check.sh PARQUET_DIR [OTHER_DIR]
#
# Fails when a table's row count differs from stats.json. With OTHER_DIR, also
# prints each table's rows on both sides and whether every row matches (an
# order-independent hash of each row); differences there are reported, not fatal.
set -euo pipefail
DIR="${1:?usage: data-check.sh PARQUET_DIR [OTHER_DIR]}"
OTHER="${2:-}"
STATS="$(cd "$(dirname "$0")/../../data" && pwd)/stats.json"
bad=0
for t in $(python3 -c "import json,sys; print(' '.join(json.load(open(sys.argv[1]))))" "$STATS"); do
  f="$DIR/$t.parquet"
  [ -f "$f" ] || continue   # tables loaded but not published (page_state, noindex_page, council_ruling)
  want=$(python3 -c "import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])" "$STATS" "$t")
  got=$(duckdb -noheader -list -c "SELECT count(*) FROM read_parquet('$f')")
  if [ "$want" != "$got" ]; then echo "data-check: $t has $got rows, stats.json says $want" >&2; bad=1; fi
done
[ "$bad" = 0 ] && echo "data-check: row counts match stats.json"
if [ -n "$OTHER" ]; then
  for f in "$DIR"/*.parquet; do
    n=$(basename "$f" .parquet); g="$OTHER/$n.parquet"
    if [ ! -f "$g" ]; then echo "$n: only here"; continue; fi
    duckdb -noheader -list -c "
      WITH a AS (SELECT count(*) n, sum(hash(to_json(t))) h FROM read_parquet('$f') t),
           b AS (SELECT count(*) n, sum(hash(to_json(t))) h FROM read_parquet('$g') t)
      SELECT '$n' || ': ' || a.n || ' rows here, ' || b.n || ' there, ' ||
             CASE WHEN a.h = b.h THEN 'same rows' ELSE 'rows differ' END FROM a, b" 2>/dev/null ||
      echo "$n: could not compare"
  done
fi
exit "$bad"
