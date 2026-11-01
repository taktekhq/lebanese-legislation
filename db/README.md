# Database

This folder holds two things: the database's schema with the inputs the build reads (the Go package `db`), and the PostgreSQL image.

## Schema and build inputs

`db.go` embeds these into the build (`internal/build`), so a build needs nothing outside this repository:

| File | What |
| --- | --- |
| `schema.sql`, `indexes.sql`, `amendments.sql`, `graph.sql`, `official.sql` | the tables, views, search functions and indexes |
| `amendments.tsv`, `aliases.tsv`, `amounts.tsv`, `amounts_review.tsv`, `article_corrections.tsv`, `centre_errors.tsv`, `date_corrections.tsv`, `codes.tsv`, `council_rulings.tsv`, `approved_edits.tsv`, `provenance_known.tsv` | decisions a person checked: amendments kept or rejected, names that point to a text, sums a text changes, corrections to the Centre's text and dates, the principal codes, the Constitutional Council's rulings |
| `names.tsv`, `titles_en.tsv` | English and French names for the Centre's vocabularies, and unofficial English titles |
| `model/` | what a model read: citations between texts and in rulings, and judgements on amendments; matched against the data on every build |

## Image

`ghcr.io/taktekhq/lebanese-legislation-db` is PostgreSQL 18.6 with pgvector 0.8.2, built from the official `postgres:18.6` image. Both are pinned: the base by digest, the pgvector source by SHA-256.

On first start with an empty volume it reads `releases/latest.json` from the [Hugging Face dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation/tree/main/releases), downloads that dump, checks its SHA-256 and restores it. The container reports healthy only after the restore passes its checks. Later starts reuse the volume and download nothing.

| File | Role |
| --- | --- |
| `Dockerfile` | Targets `postgres` (pgvector only), `db` (the published image) and `publisher` (`build-release` and `publish-release` beside PostgreSQL 18.6 clients, used by the [release workflow](../.github/workflows/release.yml)) |
| `init-reader.sh` | Creates the read-only `lebleg_reader` role when `LEBLEG_READER_PASSWORD` is set |
| `init-release.sh` | Runs [`restore-release`](../cmd/restore-release/) once, when PostgreSQL initialises an empty volume: download, verify, restore. The restore also adds what the website, API and MCP read (`tools/src/lebleg/sql/web.sql`) |
| `health.sh` | Healthy only when PostgreSQL answers over TCP, [`check-release`](../cmd/check-release/) finds a valid restore receipt and `web.sql` has run |
| `web-schema.sh` | Adds those pieces to a database restored by 0.2.0 or older |

Build from the repository root:

```sh
docker build -f db/Dockerfile -t lebanese-legislation-db .
```

Settings, pinning a release and retrying a failed restore are in the [service reference](../docs/reference.md).
