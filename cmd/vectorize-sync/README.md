# Sync vectors to Cloudflare Vectorize

From the repository root, with Go installed, set `CLOUDFLARE_ACCOUNT_ID`,
`LEBLEG_VECTORIZE_INDEX` and `LEBLEG_VECTORIZE_TOKEN` (Vectorize Write permission).
The index must already exist with 768 dimensions and cosine distance.

```sh
go run ./cmd/vectorize-sync -in vectors.ndjson -prev previous.tsv -manifest next.tsv
```

Use the NDJSON from [export-vectors](../export-vectors). A missing previous
manifest means a first run. Manifests contain `id<TAB>hash`, one row per vector.
New or changed rows are upserted; absent IDs are deleted. Four workers send
batches of at most 1,000 vectors or IDs. Only IDs and hashes remain in memory.
The output manifest is replaced atomically after all requests succeed. Preserve
the previous manifest on failure and rerun; mutations are idempotent.
Vectorize applies accepted mutations asynchronously.

`-dry-run` prints total, upsert, unchanged and delete counts without credentials,
uploading or writing a manifest. Defaults: `-in vectors.ndjson`, no `-prev`,
`-manifest vectors-manifest.tsv`.

The [vectors workflow](../../.github/workflows/vectors.yml) runs `build`, then
`publish` and `vectorize`. It exports the saved vectors with `cmd/export-vectors`,
publishes `vectors/vectors.parquet` openly to Hugging Face and loads Vectorize
index `lebleg-chunks`. Later runs upload only changed vectors. The published file
has 1,433,051 chunks, 768 dimensions, model `gemini-embedding-001`, and columns
`id`, `document`, `model`, `hash`, `values` (`FLOAT[768]`). See the
[DuckDB example](../../docs/reference.md#vectors).

To serve queries through Vectorize, set the three environment variables above
with a token that permits reads. Unset `LEBLEG_VECTORIZE_INDEX` to use pgvector.
The origin container defaults to `DROP_VECTORS=1`; build with
`--build-arg DROP_VECTORS=0` to retain local embeddings for pgvector search.
The container creates an expression index matching `internal/vectorize.ChunkIDSQL`
on the UTF-8 release database. Other databases can use the same expression index
to accelerate row retrieval. The model filter still applies to retrieved rows.

The code is covered by the repository's [MIT license](../../LICENSE).
