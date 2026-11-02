# Origin images and deployment

`data.Dockerfile` restores the two database dumps, prepares the search index,
shrinks the WAL and includes scans. The `data-image` workflow downloads its inputs
from R2 and publishes `ghcr.io/taktekhq/lebanese-legislation-data:YYYY-MM-DD` and
`:latest` on demand or after a successful `vectors` run.

`Dockerfile` builds the Go server and adds it and `start.sh` to the data image.
The `origin` workflow takes `data_tag` (default `latest`), or an already pushed
Cloudflare registry `image` to skip building. Each built site image has a unique
commit, run and attempt tag. Runs are serialized.

The site Worker has service bindings to `lebleg-origin-blue` and
`lebleg-origin-green`, the two environments of `workers/origin`. Each owns one
`Origin` container instance named `main`. Neither origin Worker has public routes
or a workers.dev address. Deploying an origin Worker only restarts its own side.

The workflow runs these jobs:

1. `image` builds and pushes the site image from the data image, unless `image`
   was supplied.
2. `site-worker` optionally deploys `workers/static-site` when
   `deploy_site_worker` is true (default false). Only this job applies the `route`
   and `mode` inputs and sets `ORIGIN_TEST_KEY` on the site Worker.
3. `stage` reads R2 `_build/live-side` (default blue), deploys only the idle
   environment with the new image, and sets its `GEMINI_API_KEY` and
   `LEBLEG_VECTORIZE_TOKEN` secrets. The live environment is never deployed.
4. `warm` checks the idle side through the site Worker for up to 30 minutes.
   Probes keep the named `lebleg-origin-deploy/1` User-Agent.
5. `switch` writes the idle name to `_build/live-side` and waits 20 seconds for
   the site Worker's 15 second memory cache to expire. It does not deploy.
6. `verify` checks the public site for up to 60 seconds. Failure writes the old
   side back to the flag and waits 20 seconds. Success retires the old container
   through `GET /_origin/restart?side=<old>` with the test header.

`origin-state` is a short-lived workflow artifact recording the old and idle side
and the new image. Switch and rollback only need the side names.
`ORIGIN_MODE` and `AI_TO_HETZNER` retain their existing behavior.

The workflow requires `CLOUDFLARE_WORKERS_TOKEN`, `R2_ACCESS_KEY_ID`,
`R2_SECRET_ACCESS_KEY`, `ORIGIN_TEST_KEY` and `LEBLEG_GEMINI_API_KEY`.
The Vectorize token uses `CLOUDFLARE_WORKERS_TOKEN`. Origin secrets are set per
environment. The data workflow uses `GITHUB_TOKEN` with packages write
permission. The site build uses packages read permission to pull the data image.

## First production migration

Before deploying the new site Worker, deploy both origin environments with the
current production image and set both secrets on each. Warm each `main` instance
and pass all three checks through a temporary local service-binding probe Worker
(`wrangler dev --remote` with both bindings), since the old site Worker still
addresses its own containers. Stop the probe after checking. Write `blue` to R2
`_build/live-side`, then deploy the new site Worker with the existing production
routes, the intended mode and `ORIGIN_TEST_KEY`. This deployment deletes the old
container classes, so both replacement Workers must already exist and the selected
side must be warm. Verify public traffic before running routine idle-side deploys.
The probe must forward to the same `main` instance used by each origin Worker.

Run local checks from the repository root:

```sh
node --test workers/static-site/test/*.test.mjs
node --test workers/origin/test/*.test.mjs
python3 -m unittest discover -s workers/static-site/test -p 'origin_deploy_test.py'
go build ./...
```

An authenticated `x-origin-test` request can choose `x-origin-side: blue|green`.
These requests bypass static pages and API caches. The log and restart endpoints
use `?side=blue|green` with the same secret header. Normal visitors follow the R2
flag. API edge cache keys include the selected side.
