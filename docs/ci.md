# Workflows that need keys

Some jobs need a secret: an upload token, a model API key, or Cloudflare
credentials. Each is a GitHub Actions workflow in `.github/workflows/` that does
nothing harmful without its secret: it says which secret is missing and stops.

On this repository the secrets live in GitHub, so these workflows run there.
In a fork, or to try one before merging, run it on your own machine with
[act](https://github.com/nektos/act), which runs a workflow in Docker the way
GitHub does; the workflow stays the same.

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
| `hf-card.yml` | Uploads the dataset card, `METHODS.md` and `SCHEMA.md` from `.github/huggingface/` when they change on main, keeping the card's release block. | `HF_TOKEN` |
| `vectors.yml` | Embeds missing chunks, publishes `vectors/vectors.parquet` and syncs Vectorize. | `LEBLEG_GEMINI_API_KEY`, `HF_TOKEN`, R2 and Cloudflare tokens |
| `stats.yml` | Daily GitHub and Hugging Face counters to PostHog. | `GH_STATS_TOKEN` (optional, for traffic) |

On GitHub, `HF_TOKEN` lives only in the `huggingface` environment, which
deploys from `main` only, and the token can write to the
`taktekhq/lebanese-legislation` dataset and nothing else. Jobs that publish
(`data.yml` publish, `hf-card.yml`, `vectors.yml` publish) use that environment
on `main`; on other branches they run without the token and upload nothing.

Under act, run it from a checkout whose git objects are all its own (no
`--reference` alternates): the container cannot see paths outside the checkout.
Files it writes in the checkout belong to root afterwards; give them back with
`docker run --rm -v "$PWD:/d" alpine chown -R "$(id -u):$(id -g)" /d`.

[`scripts/nightly.sh`](../scripts/nightly.sh) runs on a maintainer's machine,
never in Actions: it rebuilds `data/` and opens a data-only pull request. See
[Update the data](update-data.md).
The `checks.yml` workflow runs once per pull request commit and on pushes to
main; its final `ci-ok` job requires its four check jobs (`unit`, `postgres`,
`images`, `serving`) to succeed. Its jobs check out without `data/`, so a pull
request is checked in about 3 minutes. `db.yml` bakes `data/` into the `-db` image and checks its first
boot, only when `data/`, `db/` or a Go package built into the image changes;
`.github/scripts/db-paths-check.sh` (run by `checks.yml`) fails when a package
in the image is missing from its path lists. `data.yml` publishes Parquet tables on Hugging Face. PostgreSQL is distributed
in the [`-db` and `-all` images](../README.md#images).

## Cloudflare hosting and vectors

See [architecture](architecture.md) for the current request and build paths.

| Workflow | What it does |
| --- | --- |
| [static-site.yml](../.github/workflows/static-site.yml) | Renders about 760,000 pages from `data/` with `cmd/static-export`, in 24 parallel shards, into R2 bucket `lebleg-site`. |
| [static-site-catchup.yml](../.github/workflows/static-site-catchup.yml) | When a deploy finishes and every two hours, starts static-site when the live site is behind main and nothing is queued or running; reruns a failed deploy's failed jobs once. |
| [deploy-preview.yml](../.github/workflows/deploy-preview.yml) | On pull requests that touch the site's sources, comments which pages merging would render. |
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
| `-db` | `data.yml` | After `publish` succeeds on main. Also tagged `data-<hash>` for the static render. |
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
