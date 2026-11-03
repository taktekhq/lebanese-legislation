#!/usr/bin/env bash
# Requires Bash, curl and grep. Override BASE_URL to check another deployment.
set -euo pipefail

BASE_URL=${BASE_URL:-https://lebaneselegislation.com}
BASE_URL=${BASE_URL%/}
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT
failed=0

has_class() {
  grep -Eq "class=[\"']([^\"']*[[:space:]])?$1([[:space:]][^\"']*)?[\"']" "$work_dir/body"
}

check() {
  local name=$1 path=$2 expected=$3 question=$4 ai=$5
  local started=$SECONDS status reason='' curl_status=0
  local -a form=()
  if [[ -n $question ]]; then
    form=(--data-urlencode "q=$question")
  fi
  status=$(curl --silent --show-error --max-time 90 --retry 2 \
    --request POST "${form[@]}" --output "$work_dir/body" \
    --write-out '%{http_code}' "$BASE_URL$path" 2> "$work_dir/error") || curl_status=$?
  if (( curl_status != 0 )); then
    reason="curl exited $curl_status: $(tr '\n' ' ' < "$work_dir/error")"
  elif [[ $status != "$expected" ]]; then
    reason="expected HTTP $expected"
  elif [[ $ai == yes ]]; then
    if ! has_class ai-box__text; then
      reason='missing class ai-box__text'
    elif ! has_class ai-box__ref; then
      reason='missing class ai-box__ref'
    fi
  elif [[ $ai == no ]] && grep -Fq 'ai-box' "$work_dir/body"; then
    reason='unexpected AI box in lookup response'
  fi
  if [[ -n $reason ]]; then
    printf 'FAIL %s: HTTP %s, %ss, %s\n' "$name" "$status" "$((SECONDS - started))" "$reason"
    failed=1
  else
    printf 'PASS %s: HTTP %s, %ss\n' "$name" "$status" "$((SECONDS - started))"
  fi
}

# The server caches answers by question, so a fixed question would pass on a
# cache hit while Gemini is down. A day-stamped question forces a fresh answer.
stamp=$(date -u +%Y-%m-%d)
check 'Arabic answer' /search/ai/ 200 "هل يحق للمستأجر التعويض بعد براءة جزائية في $stamp" yes
check 'English answer' /en/search/ai/ 200 "can a tenant claim compensation after a criminal acquittal on $stamp" yes
check 'Lookup rejected' /search/ai/ 400 'قانون العمل' no
check 'API POST rejected' /api/v1/x 405 '' skip
exit "$failed"
