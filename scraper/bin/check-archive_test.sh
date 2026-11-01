#!/bin/bash
# check-archive against a throwaway archive repository and its origin.
set -euo pipefail
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHECK="$REPO/scraper/bin/check-archive"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT HUP INT TERM
BARE="$TMP/origin.git"
ARCH="$TMP/archive"
NOW=2026-10-26T12:00:00Z
export GIT_COMMITTER_DATE='2026-10-26T11:59:59Z' GIT_AUTHOR_DATE='2026-10-26T11:59:59Z'

git init -q --bare -b main "$BARE"
git init -q -b main "$ARCH"
git -C "$ARCH" config user.email test@example.invalid
git -C "$ARCH" config user.name Test
git -C "$ARCH" remote add origin "$BARE"
mkdir -p "$ARCH/law"
printf 'fixture\n' > "$ARCH/law/item.html"
git -C "$ARCH" add law
git -C "$ARCH" commit -q -m 'archive: law, 1 pages changed (2026-10-26)'
git -C "$ARCH" push -q -u origin main

expect_fail() {
  expected=$1; shift
  set +e
  output=$("$CHECK" --archive "$ARCH" --now "$NOW" "$@" 2>&1)
  status=$?
  set -e
  [ "$status" -eq 1 ] || { echo "expected exit 1, got $status: $output" >&2; exit 1; }
  printf '%s\n' "$output" | grep -F "$expected" >/dev/null || { echo "expected '$expected', got: $output" >&2; exit 1; }
}

output=$("$CHECK" --archive "$ARCH" --now "$NOW")
printf '%s\n' "$output" | grep -F 'last archive commit on origin/main is within 24h' >/dev/null
printf '%s\n' "$output" | grep -F 'archive files are at most 100 MB' >/dev/null

# GitHub's documented 100 MB limit is 100,000,000 bytes.
truncate -s 100000000 "$ARCH/law/item.html"
git -C "$ARCH" commit -q -am 'boundary-size archive file' && git -C "$ARCH" push -q origin main
output=$("$CHECK" --archive "$ARCH" --now "$NOW")
printf '%s\n' "$output" | grep -F 'archive files are at most 100 MB' >/dev/null
truncate -s 100000001 "$ARCH/law/item.html"
git -C "$ARCH" commit -q -am 'oversized archive file' && git -C "$ARCH" push -q origin main
expect_fail 'archive file exceeds 100 MB'

# A local commit that never reached origin does not count.
printf 'small\n' > "$ARCH/law/item.html"
GIT_COMMITTER_DATE='2026-10-24T11:00:00Z' GIT_AUTHOR_DATE='2026-10-24T11:00:00Z' git -C "$ARCH" commit -q -am 'old published'
git -C "$ARCH" push -q origin main
printf 'local\n' > "$ARCH/law/item.html"
git -C "$ARCH" commit -q -am 'local only'
expect_fail 'older than 24h'

# A recent published commit passes again.
git -C "$ARCH" push -q origin main
"$CHECK" --archive "$ARCH" --now "$NOW" >/dev/null

# No checkout at all.
set +e; out=$(LEBLEG_ARCHIVE_REPO="$TMP/none" "$CHECK" --archive "$TMP" --now "$NOW" 2>&1); st=$?; set -e
[ "$st" -eq 1 ] && printf '%s\n' "$out" | grep -F 'no archive checkout' >/dev/null
echo "check-archive_test: ok"
