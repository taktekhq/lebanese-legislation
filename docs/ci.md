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
| `release.yml` | Builds and checks a PostgreSQL release after successful `data` runs on main or manual dispatch. Missing Gemini keys skip the build; missing HF tokens skip publishing. | `GEMINI_API_KEY`, `HF_TOKEN` |
| `data.yml` | Loads `data/` into PostgreSQL, exports and checks the Parquet tables, and (job `publish`) uploads them to Hugging Face. | `HF_TOKEN` (write token for the dataset) |

On GitHub, `HF_TOKEN` lives only in the `huggingface` environment, which
deploys from `main` only, and the token can write to the
`taktekhq/lebanese-legislation` dataset and nothing else. Jobs that publish
(`data.yml` publish, `release.yml`, `vectors.yml` publish) use that environment
on `main`; on other branches they run without the token and upload nothing.

Under act, run it from a checkout whose git objects are all its own (no
`--reference` alternates): the container cannot see paths outside the checkout.
Files it writes in the checkout belong to root afterwards; give them back with
`docker run --rm -v "$PWD:/d" alpine chown -R "$(id -u):$(id -g)" /d`.

The XPS scrape-build-and-PR job rebuilds and proposes changes to `data/`.
The `checks.yml` workflow runs on pushes and pull requests; its final `ci-ok`
job requires all seven parallel check jobs to succeed. After `data` completes
successfully on `main`, `release.yml` builds and checks the release, then
publishes it when its keys are available. Manual dispatch remains available.

## Cloudflare hosting and vectors

See [architecture](architecture.md) for the current request and build paths.

| Workflow | What it does |
| --- | --- |
| [static-site.yml](../.github/workflows/static-site.yml) | Renders about 760,000 pages from `data/` with `cmd/static-export`, in 8 parallel shards, into R2 bucket `lebleg-site`. |
| [origin.yml](../.github/workflows/origin.yml) | Deploys one Cloudflare Container with PostgreSQL 18.6, a baked-in database and the Go website server. |
| [vectors.yml](../.github/workflows/vectors.yml) | `build` then `publish` and `vectorize`: exports vectors, publishes `vectors/vectors.parquet` to Hugging Face and loads Vectorize index `lebleg-chunks`. Later syncs upload only changed vectors. |
