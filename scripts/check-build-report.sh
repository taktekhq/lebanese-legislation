#!/bin/bash
# Check the latest build report before anything is exported.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPORT="${BUILD_REPORT:-$HERE/scraper/data/logs/build-report.txt}"

if [ ! -f "$REPORT" ]; then
  echo "$REPORT: missing build report" >&2
  exit 2
fi

# GNU stat on Linux, BSD stat on macOS.
mtime=$(stat -c %Y "$REPORT" 2>/dev/null || stat -f %m "$REPORT" 2>/dev/null) || { echo "$REPORT: cannot read modification time" >&2; exit 2; }
now=$(date +%s)
age=$((now - mtime))
if [ "$age" -gt $((20 * 60 * 60)) ]; then
  echo "$REPORT: stale (last written $(date -d "@$mtime" '+%Y-%m-%d %H:%M:%S %Z' 2>/dev/null || date -r "$mtime" '+%Y-%m-%d %H:%M:%S %Z'))" >&2
  exit 1
fi

last_flag=$(grep -n '^publishable:' "$REPORT" | tail -1 || true)
if [ -z "$last_flag" ]; then
  echo "$REPORT: no publishable: line" >&2
  exit 1
fi
flag=${last_flag#*:}
if [ "$flag" != 'publishable: true' ]; then
  line=${last_flag%%:*}
  echo "$REPORT:$line: expected publishable: true, got ${flag# }" >&2
  exit 1
fi

line=$(grep -Ein 'deadlock|graph_edge_pkey' "$REPORT" | head -1 || true)
if [ -n "$line" ]; then
  number=${line%%:*}
  text=${line#*:}
  echo "$REPORT:$number: forbidden build error: $text" >&2
  exit 1
fi

exit 0
