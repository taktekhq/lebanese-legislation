#!/usr/bin/env bash
# One Hugging Face commit per publish: every commit to the dataset restarts its
# viewer, Parquet and Croissant for about 33 minutes (audit, 4 Nov 2026). When
# this commit also ran the data workflow, its publish job uploads the card and
# this commit's vectors together with the tables, so wait for it here; the
# upload that follows is then empty and hf skips it ("No files have been
# modified"). With no data run for this commit, go on at once.
set -euo pipefail
sleep 20 # the data run of the same push may take a moment to be listed
run=$(gh api "repos/$GITHUB_REPOSITORY/actions/workflows/data.yml/runs?head_sha=$GITHUB_SHA&event=push" --jq '.workflow_runs[0].id // empty')
if [ -z "$run" ]; then
  echo "No data run for $GITHUB_SHA: uploading now."
  exit 0
fi
for _ in $(seq 120); do
  state=$(gh api "repos/$GITHUB_REPOSITORY/actions/runs/$run/jobs" --jq '[.jobs[] | select(.name == "publish")][0] | (.status // "") + " " + (.conclusion // "")')
  case "$state" in
    completed*) echo "Data run $run publish: $state."; exit 0 ;;
  esac
  sleep 30
done
echo "Data run $run did not publish within 60 minutes: uploading now."
