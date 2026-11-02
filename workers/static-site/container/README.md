# Origin images and deployment

`data.Dockerfile` restores the two database dumps, prepares the search index,
shrinks the WAL and includes scans. The `data-image` workflow downloads its inputs
from R2 and publishes `ghcr.io/taktekhq/lebanese-legislation-data:YYYY-MM-DD` and
`:latest` on demand or after a successful `vectors` run.

`Dockerfile` builds the Go server and adds it and `start.sh` to the data image.
The `origin` workflow takes `data_tag` (default `latest`), or an already pushed
Cloudflare registry `image` to skip building. Each built site image has a unique
commit, run and attempt tag. It stages the idle class, waits up to 30 minutes for
all three origin checks, switches LIVE, verifies publicly for up to 60 seconds,
then rolls back on failure or stops the old side on success. Runs are serialized.
`ORIGIN_MODE` and `AI_TO_HETZNER` retain their existing behavior.

The workflow reads LIVE from the current Worker deployment and keeps the live
image in R2 `_build/live-image` alongside `_build/live-side`. On the first run it
resolves the existing Origin image from its Cloudflare container application.
`origin-state` is a short-lived workflow artifact holding both image pins for
switch and rollback. All deploy jobs use those same pins.

The workflow requires `CLOUDFLARE_WORKERS_TOKEN`, `R2_ACCESS_KEY_ID`,
`R2_SECRET_ACCESS_KEY` and `ORIGIN_TEST_KEY`; it also installs the existing Gemini
and Vectorize secrets. The data workflow uses `GITHUB_TOKEN` with packages write
permission. The site build uses packages read permission to pull the data image.

Run local checks from the repository root:

```sh
node --test workers/static-site/test/*.test.mjs
python3 -m unittest discover -s workers/static-site/test -p '*_test.py'
go build ./...
```

An authenticated `x-origin-test` request can choose `x-origin-side: blue|green`.
These requests bypass static pages and API caches. The log and restart endpoints
use `?side=blue|green` with the same secret header. Normal visitors follow LIVE.
