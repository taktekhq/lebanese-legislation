#!/usr/bin/env bash
set -euo pipefail
: "${MCP_IMAGE:?Set MCP_IMAGE}"
: "${MCP_EXPECTED_VERSION:?Set MCP_EXPECTED_VERSION}"
container=""
cleanup() {
  if [[ -n "$container" ]]; then docker rm -fv "$container" >/dev/null; fi
}
trap cleanup EXIT
for architecture in ${MCP_ARCHITECTURES:-amd64 arm64}; do
  container="lebleg-mcp-smoke-$architecture-$$"
  # Initialization and ping need no live database. No provider key is supplied.
  docker run -d --platform "linux/$architecture" --name "$container" \
    -e DATABASE_URL='host=127.0.0.1 port=9 user=smoke dbname=smoke' "$MCP_IMAGE" >/dev/null
  for attempt in $(seq 1 30); do
    if docker exec -i -e MCP_EXPECTED_VERSION="$MCP_EXPECTED_VERSION" "$container" python - <<'PY'
import json
import os
import urllib.error
import urllib.request

base = 'http://127.0.0.1:8000'
message = {'jsonrpc':'2.0','id':1,'method':'initialize','params':{'protocolVersion':'2025-06-18'}}
request = urllib.request.Request(base+'/mcp', data=json.dumps(message).encode(), headers={'Content-Type':'application/json'})
try:
    response = urllib.request.urlopen(request, timeout=2)
except urllib.error.HTTPError:
    raise
except urllib.error.URLError:
    raise SystemExit(1) from None
with response:
    assert json.load(response)['result']['serverInfo']['version'] == os.environ['MCP_EXPECTED_VERSION']
for path in ['/api/v1/', '/search?q=source', '/docs', '/openapi.json']:
    try:
        urllib.request.urlopen(base+path, timeout=2)
    except urllib.error.HTTPError as error:
        assert error.code == 404, (path, error.code)
    else:
        raise AssertionError(path+' unexpectedly exposed')
PY
    then
      break
    fi
    if [[ "$attempt" == 30 ]]; then
      echo "MCP HTTP smoke failed on linux/$architecture" >&2
      exit 1
    fi
    sleep 1
  done
  cleanup
  container=""
  echo "linux/$architecture: default MCP initialization and restricted HTTP surface passed"
done
