#!/bin/bash
# Nightly: rebuild the database from the archive and, when the build passes
# its checks, export data/ and propose what changed in a pull request.
# A failed gate publishes nothing.
#
# Adapted from deploy/nightly.sh: local scraping and checks, then a data PR.
# Private reporting and publishing are excluded.
set -euo pipefail
if [ "${GITHUB_ACTIONS+x}" = x ]; then
  echo "nightly: run on your own machine, never in GitHub Actions" >&2
  exit 1
fi
DRY_RUN=0
case "${1:-}" in
  --dry-run) DRY_RUN=1; shift ;;
  --help) echo "Usage: scripts/nightly.sh [--dry-run] (see docs/update-data.md)"; exit 0 ;;
esac
[ "$#" -eq 0 ] || { echo "nightly: unknown argument: $1" >&2; exit 2; }
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -z "${LEBLEG_LOCKED:-}" ] || [ -z "${NIGHT:-}" ]; then
  NIGHT=$(TZ="${LEBLEG_TIMEZONE:-UTC}" date +%F)
fi
export NIGHT
export LEBLEG_REPO="${LEBLEG_REPO:-taktekhq/lebanese-legislation}"
export LEBLEG_DSN="${LEBLEG_DSN:-host=localhost dbname=lebleg}"
STORE="${LEBLEG_STORE:-$REPO/scraper/data}"
DATA="${LEBLEG_DATA:-$REPO/data}"
ARCHIVE="${LEBLEG_ARCHIVE:-$REPO/archive}"
BIN="$REPO/.cache/nightly/bin"
LOG_DIR="$STORE/logs"
mkdir -p "$BIN" "$LOG_DIR"
# Hold one lock for the whole run, wait up to an hour, exit 75 if still busy.
# flock replaces the private pipeline lock command, including on a first run.
LOCKFILE="${LEBLEG_LOCK_FILE:-$STORE/nightly.lock}"
OMIT_FILE="${LEBLEG_OMIT_FILE:-$STORE/omit-next}"
if [ -z "${LEBLEG_LOCKED:-}" ]; then
  mkdir -p "$(dirname "$LOCKFILE")"
  export LEBLEG_LOCKED=1
  args=()
  [ "$DRY_RUN" -eq 0 ] || args+=(--dry-run)
  exec flock -E 75 -w "${LEBLEG_LOCK_WAIT:-3600}" "$LOCKFILE" "$0" "${args[@]}"
fi
WORKTREE=""
# Consume omit-next after any lock re-exec, for this night only.
OMIT_ONCE="${LEBLEG_OMIT_ONCE_FILE:-$LOG_DIR/pipeline/omit-once.$$}"
if [ -f "$OMIT_FILE" ]; then
  mkdir -p "$(dirname "$OMIT_ONCE")"
  cp "$OMIT_FILE" "$OMIT_ONCE"
  rm -f "$OMIT_FILE"
fi
report() {
  rc=$?
  trap '' EXIT
  rm -f "${OMIT_ONCE:-}"
  # Finish local reporting without changing the run's exit status.
  trap '' INT TERM HUP
  if [ -n "$WORKTREE" ]; then
    git -C "$REPO" worktree remove --force "$WORKTREE" || true
  fi
  # Answers read on a night that opened no PR are dropped, so the next run's
  # pull from main never stops on these files; they are asked again then.
  git -C "$REPO" checkout -- db/model/judgements.jsonl db/model/citations.jsonl 2>/dev/null || true
  mark nightly-run "$([ "$rc" -eq 0 ] && echo ran || echo failed)" "exit $rc${FAIL_WHY:+: $FAIL_WHY}" || true
  exit $rc
}
FAIL_WHY=""   # why the run failed, when a step knows: added to the nightly-run line
trap report EXIT
# A signal ends the run with its own exit code,
# so the trap marks it failed instead of with the last command's 0.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
LOG="$LOG_DIR/nightly.log"
# Local marks replace the private pipeline report and its remote posts.
mark() { printf '%s\t%s\n' "$NIGHT" "$*" >> "$LOG_DIR/steps.log" || true; }
# omitted <step>: true when tonight skips this step's call on purpose
# (LEBLEG_OMIT, or the one-night omit snapshot). It writes no mark.
omitted() {
  local ids="${LEBLEG_OMIT:-}"
  case " ${ids//,/ } " in *" $1 "*) echo "$1: omitted (LEBLEG_OMIT), no mark"; return 0 ;; esac
  if [ -f "$OMIT_ONCE" ] && grep -qx "$1" "$OMIT_ONCE"; then
    echo "$1: omitted ($OMIT_FILE), no mark"
    return 0
  fi
  return 1
}
cd "$REPO"
export PATH="$BIN:$PATH"
# Missing this input silently opens every English page, so fail closed (#99).
[ -s "$REPO/data/popular_texts.txt" ] || { echo "nightly: data/popular_texts.txt is required" >&2; exit 1; }
# DNS has intermittently failed on the nightly host. Do not let the first
# fetch consume the run: retry both required hosts every 15 minutes for 3h.
if [ "${LEBLEG_DNS_SKIP:-0}" != 1 ]; then
  dns_elapsed=0
  dns_wait=${LEBLEG_DNS_WAIT:-10800}
  dns_interval=${LEBLEG_DNS_INTERVAL:-900}
  dns_lookup() {
    if [ -n "${LEBLEG_DNS_LOOKUP:-}" ]; then
      "$LEBLEG_DNS_LOOKUP" "$1"
    elif command -v getent >/dev/null 2>&1; then
      getent hosts "$1" >/dev/null 2>&1
    else
      # macOS has no getent; use its system resolver.
      dscacheutil -q host -a name "$1" 2>/dev/null | grep -q '^ip'
    fi
  }
  while ! dns_lookup github.com || ! dns_lookup legallaw.ul.edu.lb; do
    if [ "$dns_elapsed" -ge "$dns_wait" ]; then
      echo "nightly: DNS unavailable for github.com or legallaw.ul.edu.lb after ${dns_elapsed}s" >&2
      exit 1
    fi
    wait_now=$dns_interval
    [ $((dns_elapsed + wait_now)) -le "$dns_wait" ] || wait_now=$((dns_wait - dns_elapsed))
    echo "nightly: DNS unavailable; retrying in ${wait_now}s (elapsed ${dns_elapsed}s)" >&2
    sleep "$wait_now"
    dns_elapsed=$((dns_elapsed + wait_now))
  done
fi
if [ "${LEBLEG_DNS_TEST_ONLY:-0}" = 1 ]; then
  exit 0
fi
{
  echo "== $(date -u +%FT%TZ) (night $NIGHT)"
  # A stale source is an alarm, but does not block tonight's recovery attempt.
  if age_check=$(CHECK_DATA_REPO="$REPO" "$REPO/scripts/check-data-age.sh" 2>&1); then
    echo "$age_check"
  else
    echo "$age_check" >&2
  fi
  # Update the archived baseline before adding this machine's new raw pages.
  git submodule update -q --init --remote archive \
    || echo "archive: could not update the archive submodule; building on the archive as it is"
  # Use the existing resumable scraper. Never publish or push the archive here.
  # LEBLEG_SCRAPE=0: another job scrapes the store and publishes the archive
  # (the ullaw-* timers on our host), so build from the archive as published.
  # The export writes the whole store into the archive, so point LEBLEG_STORE
  # at the full store: a partial one deletes everything it does not hold.
  if [ "${LEBLEG_SCRAPE:-1}" != 0 ]; then
    "$REPO/scraper/bin/ul-scrape" --data "$STORE" --minutes "${LEBLEG_SCRAPE_MINUTES:-180}" daily
    "$REPO/scraper/bin/ul-scrape" --data "$STORE" --minutes "${LEBLEG_ARTICLE_MINUTES:-30}" articles
    "$REPO/scraper/bin/ul-scrape" --data "$STORE" --out "$ARCHIVE" export
  else
    echo "scrape: skipped (LEBLEG_SCRAPE=0); building from the published archive"
  fi
  for command in build-db recall-report record-scan-manifest check-dates check-amounts check-english check-versions check-rulings export-data calculated-current judge citeagy; do
    go build -o "$BIN/$command" "./cmd/$command"
  done
  SCANS="${LEBLEG_SCANS:-$REPO/manual/scans}"
  mkdir -p "$SCANS"
  # The wording guard reads each scanned text's bundle files only at the bytes
  # recorded the first night they arrived (the lebgazette fetcher writes no
  # manifest). The manifest sits beside the folder, not in it. A file changed since it was recorded keeps its old
  # hash: the guard holds export until a person checks it and runs
  # `record-scan-manifest -scans <folder> -accept <id>`. Never stops the run;
  # without a manifest the guard holds the scanned texts itself.
  mrc=0
  "$BIN/record-scan-manifest" -scans "$SCANS" -manifest "$SCANS.manifest.tsv" || mrc=$?
  case "$mrc" in
    0) ;;
    4) echo "scans manifest: files changed since they were recorded (listed above); the wording guard will hold export" ;;
    *) echo "scans manifest: could not record (exit $mrc); the wording guard will hold the scanned texts" ;;
  esac
  RULINGS="${LEBLEG_RULINGS:-$REPO/manual/rulings}"
  RULING_PDFS="${LEBLEG_RULING_PDFS:-$RULINGS}"
  mkdir -p "$RULINGS"
  # One build runs steps 5 to 12. Exit 2: it finished and its checks failed (the
  # build gate); any other failure stopped it, so what comes after is held.
  brc=0
  gate_unknown=0
  build_why=""
  "$BIN/build-db" -archive "$ARCHIVE" -popular "$REPO/data/popular_texts.txt" -scans "$SCANS" -ruling-files "$RULINGS" > "$LOG_DIR/build-report.txt" 2>&1 || brc=$?
  if [ "$brc" -eq 0 ]; then
    if build_why=$(BUILD_REPORT="$LOG_DIR/build-report.txt" "$REPO/scripts/check-build-report.sh" 2>&1); then
      build_why=""
    else
      check_rc=$?
      if [ "$check_rc" -eq 1 ]; then
        brc=2
        echo "$build_why" >&2
      else
        gate_unknown=1
        echo "build report check could not run (exit $check_rc): $build_why" >&2
      fi
    fi
  fi
  case "$brc" in
    0) for s in parse graph review-queue english; do mark "$s" ran; done
       if [ "$gate_unknown" -eq 0 ]; then mark build-gate ran; fi ;;
    2) for s in parse graph review-queue english; do mark "$s" ran; done
       mark build-gate failed "${build_why:-build did not pass its checks}" ;;
    *) mark parse failed "build-db exit $brc" ;;
  esac
  if [ "$gate_unknown" -eq 1 ]; then
    mark build-gate skipped "check-build-report could not run"
    FAIL_WHY="build-gate check could not run"
    brc=3
  fi

  # Step 6's date check: a gate, it holds what comes after.
  if [ "$brc" -eq 0 ] || [ "$brc" -eq 2 ]; then
    drc=0
    "$BIN/check-dates" \
      >> "$LOG_DIR/check-dates.log" 2>&1 || drc=$?
    case "$drc" in
      0) mark dates ran ;;
      1) mark dates failed "check-dates found violations: scraper/data/logs/check-dates.log"; brc=2 ;;
      *) mark dates failed "check-dates could not run (exit $drc)"; brc=2 ;;
    esac
  fi
  # Step 8's amounts check: a gate, it holds what comes after.
  if [ "$brc" -eq 0 ] || [ "$brc" -eq 2 ]; then
    arc=0
    "$BIN/check-amounts" \
      >> "$LOG_DIR/check-amounts.log" 2>&1 || arc=$?
    case "$arc" in
      0) ;;
      1) mark graph failed "check-amounts found violations: scraper/data/logs/check-amounts.log"; brc=2 ;;
      *) mark graph failed "check-amounts could not run (exit $arc)"; brc=2 ;;
    esac
  fi
  # Step 7: ask only about inputs not already answered. Partial answers survive
  # failures and travel with the exported data in the same PR.
  failed_in() {
    tail -c +"$(($2 + 1))" "$1" 2>/dev/null | grep -Eo 'skipped [0-9]+, failed [0-9]+' | tail -1 | sed 's/.*failed //' || true
  }
  judge_new() {
    local start rc=0 fresh
    local agy_remote=() claude_remote=()
    if [ -n "${LEBLEG_AI_REMOTE:-}" ]; then
      agy_remote=(-agy-remote "$LEBLEG_AI_REMOTE")
      claude_remote=(-claude-remote "$LEBLEG_AI_REMOTE")
    fi
    start=$(wc -c < "$judge_log")
    if [ -n "${LEBLEG_AGY:-}" ]; then
      "$BIN/judge" -scope all -workers 4 -agy "$LEBLEG_AGY" "${agy_remote[@]}" \
        -model "${LEBLEG_JUDGE_MODEL:-gemini-4-argon-medium}" >> "$judge_log" 2>&1 || rc=$?
      fresh=$(tail -c +"$((start + 1))" "$judge_log" 2>/dev/null || true)
      if ! { { [ "$rc" -ne 0 ] || printf '%s\n' "$fresh" | grep -Eq 'failed [1-9]'; } \
          && printf '%s\n' "$fresh" | grep -Eiq 'quota|limit|exhausted|too many requests'; }; then
        return "$rc"
      fi
      [ -n "${LEBLEG_CLAUDE:-}" ] || return "$rc"
      echo "judge: agy hit its quota; judging the rest with Claude" >> "$judge_log"
    fi
    rc=0
    "$BIN/judge" -scope all -workers 4 -claude "$LEBLEG_CLAUDE" "${claude_remote[@]}" \
      -model "${LEBLEG_CLAUDE_MODEL:-sonnet}" >> "$judge_log" 2>&1 || rc=$?
    return "$rc"
  }
  cite_new() {
    local start rc=0 fresh
    local agy_remote=() claude_remote=()
    if [ -n "${LEBLEG_AI_REMOTE:-}" ]; then
      agy_remote=(-remote "$LEBLEG_AI_REMOTE" -remote-bin "${LEBLEG_AGY:-}")
      claude_remote=(-claude-remote "$LEBLEG_AI_REMOTE")
    fi
    start=$(wc -c < "$cite_log")
    if [ -n "${LEBLEG_AGY:-}" ]; then
      "$BIN/citeagy" -agy "$LEBLEG_AGY" "${agy_remote[@]}" \
        -model "${LEBLEG_JUDGE_MODEL:-gemini-4-argon-medium}" -batch 25 -workers 8 >> "$cite_log" 2>&1 || rc=$?
      fresh=$(tail -c +"$((start + 1))" "$cite_log" 2>/dev/null || true)
      if ! { [ "$rc" -eq 3 ] \
          || { { [ "$rc" -ne 0 ] || printf '%s\n' "$fresh" | grep -Eq 'failed [1-9]'; } \
               && printf '%s\n' "$fresh" | grep -Eiq 'quota|limit|exhausted|too many requests'; }; }; then
        return "$rc"
      fi
      [ -n "${LEBLEG_CLAUDE:-}" ] || return "$rc"
      echo "cite: agy could not read; reading the rest with Claude" >> "$cite_log"
    fi
    rc=0
    "$BIN/citeagy" -claude "$LEBLEG_CLAUDE" "${claude_remote[@]}" \
      -model "${LEBLEG_CLAUDE_MODEL:-sonnet}" -batch 25 -workers 4 >> "$cite_log" 2>&1 || rc=$?
    return "$rc"
  }
  if [ "$brc" -eq 0 ]; then
    if omitted amendments; then
      :
    elif [ -z "${LEBLEG_AGY:-}" ] && [ -z "${LEBLEG_CLAUDE:-}" ]; then
      mark amendments skipped "no AI CLI configured"
    else
      before=$(git -C "$REPO" hash-object db/model/judgements.jsonl)
      cbefore=$(git -C "$REPO" hash-object db/model/citations.jsonl)
      judge_log="$LOG_DIR/judge.log"
      cite_log="$LOG_DIR/cite.log"
      touch "$judge_log" "$cite_log"
      amend_err=""
      jstart=$(wc -c < "$judge_log")
      if ! judge_new; then
        echo "judge failed; exporting with the judgements read so far"
        amend_err="judge failed"
      fi
      jfailed=$(failed_in "$judge_log" "$jstart")
      if [ -z "$amend_err" ] && [ "${jfailed:-0}" -gt 0 ]; then
        amend_err="judge: $jfailed items failed"
      fi
      if [ "$(git -C "$REPO" hash-object db/model/judgements.jsonl)" != "$before" ]; then
        if ! "$BIN/build-db" -archive "$ARCHIVE" -popular "$REPO/data/popular_texts.txt" -scans "$SCANS" -ruling-files "$RULINGS" -only amendments >> "$LOG_DIR/build-report.txt" 2>&1; then
          amend_err="${amend_err:+$amend_err, }amendments rebuild failed"
        fi
      fi
      cstart=$(wc -c < "$cite_log")
      if ! cite_new; then
        echo "cite failed; exporting with the citations read so far"
        amend_err="${amend_err:+$amend_err, }cite failed"
      else
        cfailed=$(failed_in "$cite_log" "$cstart")
        if [ "${cfailed:-0}" -gt 0 ]; then
          amend_err="${amend_err:+$amend_err, }cite: $cfailed items failed"
        fi
      fi
      if [ -n "$amend_err" ]; then mark amendments failed "$amend_err"; else mark amendments ran; fi
      if [ "$(git -C "$REPO" hash-object db/model/judgements.jsonl)" != "$before" ] || [ "$(git -C "$REPO" hash-object db/model/citations.jsonl)" != "$cbefore" ]; then
        if "$BIN/build-db" -archive "$ARCHIVE" -popular "$REPO/data/popular_texts.txt" -scans "$SCANS" -ruling-files "$RULINGS" -only graph >> "$LOG_DIR/build-report.txt" 2>&1; then
          mark graph ran "rebuilt after judge and cite"
        else
          mark graph failed "rebuild after judge and cite failed; exporting anyway"
        fi
      fi
    fi
  fi
  # Step 22: compute the site's indexing metadata after the main build.
  # Required by issue #99: retain the English noindex rows before exporting.
  if "$BIN/build-db" -archive "$ARCHIVE" -popular "$REPO/data/popular_texts.txt" -only indexing -scans "$SCANS" -ruling-files "$RULINGS" \
      >> "$LOG_DIR/build-report.txt" 2>&1; then
    mark indexing ran
  else
    indexing_rc=$?
    mark indexing failed "indexing build failed (exit $indexing_rc)"
    brc=2
  fi
  # Step 12's English check: advisory, it never holds the night.
  if [ "$brc" -eq 0 ] || [ "$brc" -eq 2 ]; then
    erc=0
    "$BIN/check-english" -db "$LEBLEG_DSN" -repo "$REPO" \
      >> "$LOG_DIR/check-english.log" 2>&1 || erc=$?
    case "$erc" in
      0) mark english ran ;;
      1) mark english failed "check-english found violations: scraper/data/logs/check-english.log" ;;
      *) mark english failed "check-english could not run (exit $erc)" ;;
    esac
  fi
  if [ "$brc" -ne 0 ]; then
    if [ "$gate_unknown" -eq 1 ]; then
      FAIL_WHY="build-gate check could not run"
      echo "build gate could not be verified (exit $brc); nothing exported"
    else
      echo "build did not pass its checks or could not complete (exit $brc); nothing exported"
    fi
    tail -20 "$LOG_DIR/build-report.txt"
    exit 1
  fi
  if ! omitted versions; then
    if "$BIN/check-versions" -pending-file "$LOG_DIR/versions-pending.txt" >> "$LOG_DIR/build-report.txt" 2>&1; then
      mark versions ran "check-versions passed"
    else
      vrc=$?
      mark versions failed "check-versions exit $vrc"
      # Advisory until it passes a live night: it never holds the export.
      echo "check-versions failed (exit $vrc); exporting anyway"
      tail -20 "$LOG_DIR/build-report.txt"
    fi
  fi
  # Step 13's check runs against the completed DB and the same readings loaded by the build.
  if omitted rulings; then
    :
  elif "$BIN/check-rulings" -archive "$ARCHIVE" --dir "$RULINGS" --pdfs "$RULING_PDFS" > "$LOG_DIR/check-rulings.txt" 2>&1; then
    mark rulings ran "check-rulings passed"
  else
    check_rc=$?
    # awk stops reading by itself: grep | head killed grep with SIGPIPE on a
    # 44,725-line log, and pipefail made the nightly exit 141 (28 Oct 2026).
    check_reason=$(awk '/^FAIL /{print; if (++n == 5) exit}' "$LOG_DIR/check-rulings.txt" | tr '\n' ';' | cut -c1-450)
    mark rulings failed "check-rulings exit $check_rc${check_reason:+: $check_reason}"
    cat "$LOG_DIR/check-rulings.txt"
    # Advisory until it passes a live night: it never holds the export.
  fi
  # Export runs the wording guard first (internal/provenance): exit 4 when a law
  # text is not in its archived source, and nothing is written. What comes after
  # export is then held, with the reason, and the run fails with the guard's code.
  # Preserve the shard plan even when exporting to an external directory.
  mkdir -p "$DATA"
  if [ ! -f "$DATA/shards.json" ]; then
    cp "$REPO/data/shards.json" "$DATA/shards.json"
  fi
  erc=0
  "$BIN/export-data" -archive "$ARCHIVE" -scans "$SCANS" -out "$DATA" || erc=$?
  if [ "$erc" -ne 0 ]; then
    if [ "$erc" -eq 4 ]; then FAIL_WHY="wording guard blocked export"; else FAIL_WHY="export failed"; fi
    echo "$FAIL_WHY (exit $erc); nothing published"
    exit "$erc"
  fi
  # Calculated wording is part of the versioned dataset, even when source rows
  # have not changed. This command independently repeats the provenance gate.
  crc=0
  "$BIN/calculated-current" -archive "$ARCHIVE" -scans "$SCANS" -out "$DATA/article_calculated_current.jsonl" || crc=$?
  if [ "$crc" -ne 0 ]; then
    FAIL_WHY="calculated export failed (exit $crc)"
    exit "$crc"
  fi
  # End in a reviewable data and model commit in a clean, separate worktree.
  # Base it on the upstream default branch; never carry local code into the PR.
  BASE="${LEBLEG_BASE:-main}"
  REMOTE="${LEBLEG_REMOTE:-origin}"
  git fetch "$REMOTE" "$BASE"
  branch="data/$NIGHT"
  WORKTREE=$(mktemp -d "${TMPDIR:-/tmp}/lebleg-data.XXXXXXXX")
  rmdir "$WORKTREE"
  git worktree add -b "$branch" "$WORKTREE" "FETCH_HEAD"
  # Mirror the export, including deletions, without publishing anything yet.
  rsync -a --delete --include='/tables/***' --include='/TABLES' \
    --include='/schema.sql' --include='/indexes.sql' --include='/shards.json' \
    --include='/stats.json' --include='/article_calculated_current.jsonl' \
    --exclude='*' "$DATA/" "$WORKTREE/data/"
  mkdir -p "$WORKTREE/db/model"
  cp "$REPO/db/model/judgements.jsonl" "$REPO/db/model/citations.jsonl" "$WORKTREE/db/model/"
  git -C "$WORKTREE" add -A -- data/ db/model/judgements.jsonl db/model/citations.jsonl
  if git -C "$WORKTREE" diff --cached --quiet; then
    echo "no changes"
    git -C "$REPO" worktree remove "$WORKTREE"
    WORKTREE=""
    git branch -d "$branch"
    exit 0
  fi
  stats=$(git -C "$WORKTREE" diff --cached --shortstat -- data/)
  git -C "$WORKTREE" commit -m "data: $NIGHT, $stats" -- data/ db/model/judgements.jsonl db/model/citations.jsonl
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "dry run: committed $branch ($stats); no push or PR"
    exit 0
  fi
  push_attempt=1
  while ! git -C "$WORKTREE" push -u "$REMOTE" "$branch"; do
    if [ "$push_attempt" -ge 3 ]; then
      FAIL_WHY="data push failed"
      echo "$FAIL_WHY after $push_attempt attempts"
      exit 1
    fi
    echo "data push failed (attempt $push_attempt/3), retrying in 15s..." >&2
    push_attempt=$((push_attempt + 1))
    sleep 15
  done
  head="${LEBLEG_HEAD_OWNER:+$LEBLEG_HEAD_OWNER:}$branch"
  gh pr create --repo "$LEBLEG_REPO" --base "$BASE" --head "$head" \
    --title "data: $NIGHT" \
    --body "Nightly scrape and rebuild. $stats. Build gates, indexing and provenance checks ran before export. Model judgements and citations read tonight are in db/model/. Maintainers merge this PR; workflows publish the data."
} >> "$LOG" 2>&1
