#!/bin/bash
# Alert when either public Hugging Face data or the public data checkout is stale.
# Exit 0 means both are at most 30 hours old; exit 1 means stale; exit 2 means
# the check could not obtain a timestamp.
set -u
REPO="${HF_REPO:-taktekhq/lebanese-legislation}"
API="${CHECK_DATA_HF_API:-https://huggingface.co/api/datasets/$REPO}"
DATA="${CHECK_DATA_REPO:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
MAX_AGE="${CHECK_DATA_MAX_AGE:-108000}"
NOW="${CHECK_DATA_NOW:-$(date +%s)}"

fail() { echo "check-data-age: FAIL $*"; exit 1; }
error() { echo "check-data-age: ERROR $*" >&2; exit 2; }
command -v curl >/dev/null 2>&1 || error "curl is missing"
command -v python3 >/dev/null 2>&1 || error "python3 is missing"
command -v git >/dev/null 2>&1 || error "git is missing"
if [ ! -d "$DATA/.git" ] && [ ! -f "$DATA/.git" ]; then
  error "data repo checkout is missing at $DATA"
fi
api=$(curl -fsS -m 60 "$API") || error "cannot read $API"
hf_epoch=$(printf '%s' "$api" | python3 -c '
import datetime, json, sys
try:
    value = json.load(sys.stdin)["lastModified"]
    t = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    print(int(t.timestamp()))
except (KeyError, ValueError, TypeError):
    raise SystemExit(1)
') || error "Hugging Face lastModified is missing or invalid"
data_epoch=$(git -C "$DATA" log -1 --format=%ct -- data/ 2>/dev/null) || error "cannot read data repo commit time at $DATA"
for value in "$hf_epoch" "$data_epoch" "$NOW" "$MAX_AGE"; do
  case "$value" in ''|*[!0-9]*) error "invalid timestamp or age" ;; esac
done
hf_age=$((NOW - hf_epoch))
data_age=$((NOW - data_epoch))
[ "$hf_age" -ge 0 ] || error "Hugging Face timestamp is in the future"
[ "$data_age" -ge 0 ] || error "data repo commit is in the future"
[ "$hf_age" -le "$MAX_AGE" ] || fail "Hugging Face dataset is ${hf_age}s old (limit ${MAX_AGE}s)"
[ "$data_age" -le "$MAX_AGE" ] || fail "data repo is ${data_age}s old (limit ${MAX_AGE}s)"
echo "check-data-age: ok (Hugging Face ${hf_age}s, data repo ${data_age}s)"
