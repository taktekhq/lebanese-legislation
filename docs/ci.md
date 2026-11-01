# Workflows that need keys

Some jobs need a secret: an upload token, a model API key, or the server's
SSH key. Each is a GitHub Actions workflow in `.github/workflows/` that does
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
