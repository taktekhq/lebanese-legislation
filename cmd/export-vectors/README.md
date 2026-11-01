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
