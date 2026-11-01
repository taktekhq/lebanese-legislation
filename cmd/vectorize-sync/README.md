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

The `vectors` GitHub Actions workflow restores the R2 search dump, publishes
`vectors/vectors.parquet` to Hugging Face when `HF_TOKEN` is set, then syncs
Vectorize and saves the manifest in R2. It runs only with `workflow_dispatch`.

To serve queries through Vectorize, set the three environment variables above
with a token that permits reads. Unset `LEBLEG_VECTORIZE_INDEX` to use pgvector.
The origin container defaults to `DROP_VECTORS=1`; build with
`--build-arg DROP_VECTORS=0` to retain local embeddings for pgvector search.
The container creates an expression index matching `internal/vectorize.ChunkIDSQL`
on the UTF-8 release database. Other databases can use the same expression index
to accelerate row retrieval. The model filter still applies to retrieved rows.

The code is covered by the repository's [MIT license](../../LICENSE).
