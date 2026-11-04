# Workflows that need keys

Some jobs need a secret: an upload token, a model API key, or Cloudflare
credentials. Each is a GitHub Actions workflow in `.github/workflows/` that does
nothing harmful without its secret: it says which secret is missing and stops.

Until the secrets live in GitHub, we run these workflows on our own machine
with [act](https://github.com/nektos/act), which runs a workflow in Docker
the way GitHub does. Moving one to GitHub later means adding its secrets in
the repository settings; the workflow stays the same.

## Run one with act

Install act (0.2.89 or later) and Docker. From the repository root:

```sh
act workflow_dispatch -W .github/workflows/data.yml --input publish=true \
  -P ubuntu-24.04=ghcr.io/catthehacker/ubuntu:act-24.04 \
  --secret-file ~/.config/lebleg/ci.secrets \
  --artifact-server-path /tmp/act-artifacts -b
```

- `-b` runs on this checkout instead of a copy, so `archive/` is not copied.
- `--secret-file` is a file of `NAME=value` lines, outside the repository.
- `--artifact-server-path` lets one job hand files to the next.

## The workflows and their secrets

| Workflow | What it does | Secrets |
| --- | --- | --- |
| `data.yml` | Loads `data/` into PostgreSQL, exports and checks the Parquet tables, and (job `publish`) uploads them to Hugging Face. | `HF_TOKEN` (write token for the dataset) |

On GitHub, `HF_TOKEN` lives only in the `huggingface` environment, which
deploys from `main` only, and the token can write to the
`taktekhq/lebanese-legislation` dataset and nothing else. Jobs that publish
(`data.yml` publish, `vectors.yml` publish) use that environment
on `main`; on other branches they run without the token and upload nothing.

Under act, run it from a checkout whose git objects are all its own (no
`--reference` alternates): the container cannot see paths outside the checkout.
Files it writes in the checkout belong to root afterwards; give them back with
`docker run --rm -v "$PWD:/d" alpine chown -R "$(id -u):$(id -g)" /d`.

The XPS scrape-build-and-PR job rebuilds and proposes changes to `data/`.
The `checks.yml` workflow runs on pull requests and pushes to main; its final
`ci-ok` job requires all four parallel check jobs to succeed. Its jobs check out
without `data/`. `db.yml` bakes `data/` into the `-db` image and checks its first
boot, only when `data/`, `db/` or a Go package built into the image changes;
`.github/scripts/db-paths-check.sh` (run by `checks.yml`) fails when a package
in the image is missing from its path lists. `data.yml` publishes Parquet tables on Hugging Face. PostgreSQL is distributed
in the [`-db` and `-all` images](../README.md#images).

## Cloudflare hosting and vectors

See [architecture](architecture.md) for the current request and build paths.

| Workflow | What it does |
| --- | --- |
| [static-site.yml](../.github/workflows/static-site.yml) | Renders about 760,000 pages from `data/` with `cmd/static-export`, in 24 parallel shards, into R2 bucket `lebleg-site`. |
| [origin.yml](../.github/workflows/origin.yml) | Deploys one Cloudflare Container with PostgreSQL 18.6, a baked-in database and the Go website server. |
| [vectors.yml](../.github/workflows/vectors.yml) | `build` then `publish` and `vectorize`: exports vectors, publishes `vectors/vectors.parquet` to Hugging Face and loads Vectorize index `lebleg-chunks`. Later syncs upload only changed vectors. |

## Daily live AI answer check

[ai-answer-check.yml](../.github/workflows/ai-answer-check.yml) runs daily at
05:47 UTC and supports manual runs. It checks Arabic and English AI answers
for answer text and references, rejects a plain law lookup with HTTP 400 and
no AI box, and checks that an API POST returns HTTP 405. It never calls
`/deep/` and costs about $0.01 a day. Failures open or update one GitHub issue;
a successful run comments `Recovered` and closes it.

Run it locally with Bash, curl and grep:

```sh
bash scripts/check-ai-answer.sh
# Optional: check another deployment
BASE_URL=https://your-deployment.example bash scripts/check-ai-answer.sh
```

Each check prints its HTTP status and elapsed seconds. Any failure exits 1
with a reason. The workflow uses GitHub's token to manage issues and needs
no model API key.

## Image publication

Each producer calls [build-image.yml](../.github/workflows/build-image.yml) as a
separate job with inherited secrets, `contents: read` and `packages: write`.
The reusable workflow logs into GHCR with `GITHUB_TOKEN`, builds with Buildx,
pushes tags and records the digest in its job summary.

| Image | Built in workflow | When |
| --- | --- | --- |
| `-db` | `data.yml` | After `publish` succeeds on main. R2 supplies scans. |
| `-api` | `checks.yml` (`checks`) | Push to main after `ci-ok` succeeds. |
| `-mcp` | `checks.yml` (`checks`) | Push to main after `ci-ok` succeeds. |
| `-all` | `data.yml`, `checks.yml` | After `-db`, or push to main after `ci-ok`. Pulls `-db:latest`. |
| `-vectors` | `vectors.yml` | After `publish` succeeds on main. |

Each image uses its own `images-<name>` job concurrency group, shared across
callers, with cancellation for pushes. There is no workflow-wide image lock.
Producer jobs keep separate concurrency groups. API and MCP still pass smoke checks on amd64 and arm64
before promotion to date, latest and available immutable release tags.

[images.yml](../.github/workflows/images.yml) is a manual button only. Select
one suffix with its `image` input: `db`, `api`, `mcp`, `all` or `vectors`.
It has no push or workflow completion triggers and no changes-selection job.

[origin.yml](../.github/workflows/origin.yml) listens for successful `checks`
and `data` completions on main, checks that `all / image-all` succeeded, then
deploys `-all:latest` through the existing blue/green stages. Manual dispatch
still accepts a published tag or an existing Cloudflare registry image.
