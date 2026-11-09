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

Both `lebleg` and `lebleg_search` are baked in at build time from `data/`.
The former loads `data/load.sh`, `official.sql` and the website SQL; the latter
uses `cmd/build-search` and the existing semantic corpus, hashes and chunker.
There are no embedding calls and no embedding column. The accepted comparison
matched 1,547,916 of 1,547,937 published IDs, with 0 extra and 21 stale IDs.
The vectors workflow removes those stale IDs from Vectorize on its next sync.

Postgres runs by default. On first start, `start.sh` copies the baked cluster
into an empty `PGDATA` volume and configures `POSTGRES_USER`,
`POSTGRES_PASSWORD` and `LEBLEG_READER_PASSWORD`. Later starts reuse it.
`lebleg_web` and `lebleg_reader` have read access to both databases.
A new image does not overwrite a populated volume. Back up changes and use a
fresh volume when replacing the dataset. No release download happens at startup.

`Dockerfile` retains the `postgres` target for tests and `publisher` for
legacy contributor tools and image checks. Those targets have no baked data. The default `db`
target includes the data and runs `ANALYZE`. After a clean shutdown, the build
resets WAL on this new cluster without forcing it, then copies only the finished
cluster into the final image. This excludes the bulk load's spare WAL segments
from published layers. It never resets WAL on a user's running database.
Gazette scans are linked to the original PDFs on SaderLex, not hosted or included in the images.
`health.sh` checks PostgreSQL and both databases' serving tables.

### -db and -db-prod

`ghcr.io/taktekhq/lebanese-legislation-db` is the public dataset image: the
database exactly as the published data defines it. Pull it for the data.
`ghcr.io/taktekhq/lebanese-legislation-db-prod` is `-db` plus read-optimized
copies that the MCP server reads, built by `prod/Dockerfile`
from one SQL file, `prod.sql`: `law_card` (the law summary every MCP reply
carries), `article_card` (each article with its wording in force resolved),
`possibly_affected_t` (the rows of the `possibly_affected` view as a table; the view is untouched),
`law_link` (`get_law`'s amended_by and amended lists, in order), `search_known`
(the codes and unserved texts `search_legislation` reads once) and `law_pub_count`
(running counts of texts by kind and publication day, `list_legislation`'s total).
No new data, and `prod.sql` can rebuild them at any time. The all-in-one image
`-all`, which runs lebaneselegislation.com, is built from `-db-prod`. Measured
on 9 Nov 2026 at the live limits (1 CPU, 6 GB), 6 `get_article` calls at once
went from a p95 of 1,192 ms to 85 ms, with byte-identical replies.

```sh
docker build -f db/Dockerfile -t lebanese-legislation-db .
docker run --rm -p 127.0.0.1:5433:5432 -e POSTGRES_PASSWORD=local lebanese-legislation-db
```

Read [loading vectors](../vectors/README.md) to add semantic search to your own
Vectorize index or to pgvector. Portable source tables are available as
[Parquet on Hugging Face](https://huggingface.co/datasets/taktekhq/lebanese-legislation).
