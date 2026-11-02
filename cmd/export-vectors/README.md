# Export semantic vectors

From the repository root, with Go installed and a PostgreSQL database containing
`public.semantic_chunks` and pgvector:

```sh
go run ./cmd/export-vectors -dsn 'postgresql://user:password@localhost/database' -out vectors.ndjson
```

`-dsn` defaults to `LEBLEG_VECTOR_DSN`, then `cli.DefaultDSN()` (`LEBLEG_DSN`
or `host=/tmp dbname=lebleg`). A read-only cursor fetches 1,000 rows at a time.
Each NDJSON row contains `id`, `document` (`kind:source_id`), `model`, `hash`
and 768 float values. IDs join the full chunk primary key with `:`; keys longer
than 64 bytes use the first 16 SHA-256 bytes as 32 hex characters.

The code is covered by the repository's [MIT license](../../LICENSE).

## Published vectors

[`vectors/vectors.parquet`](https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet) contains 1,547,937 semantic search chunks,
embedded with `gemini-embedding-001` in 768 dimensions. Columns: `id`,
`document`, `model`, `hash`, `values` (`FLOAT[768]`). The file is published openly
in the `taktekhq/lebanese-legislation` Hugging Face dataset.

Read it directly with DuckDB:

```sql
SELECT id, document, model, hash, values
FROM read_parquet('https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet')
LIMIT 5;
```

The hosted search uses Cloudflare Vectorize index `lebleg-chunks` instead of
storing vectors in PostgreSQL. The [vectors workflow](https://github.com/taktekhq/lebanese-legislation/blob/main/.github/workflows/vectors.yml)
runs `build`, then `publish` and `vectorize`. `cmd/export-vectors` exports the
chunks; `cmd/vectorize-sync` uploads only changed vectors on later runs.
