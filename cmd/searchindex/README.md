# searchindex

This command builds the PostgreSQL source index for export or local pgvector use.
Hosted semantic search uses Cloudflare Vectorize index `lebleg-chunks`. The
[vectors workflow](../../.github/workflows/vectors.yml) exports and publishes
`vectors/vectors.parquet` on Hugging Face and syncs Vectorize with only changed
vectors uploaded on later runs. See [architecture](../../docs/architecture.md).

Build a multilingual semantic search index from the canonical LebaneseLegislation corpus. The corpus stays authoritative. The index lives in a separate PostgreSQL database with pgvector 0.8 or newer, so a nightly corpus database replacement preserves vectors. Embeddings retain 768 dimensions and are validated and normalized as float32 before storage. PostgreSQL stores them as `halfvec(768)` with `halfvec_cosine_ops`, reducing the size of stored vectors and the ANN index without another embedding API call.

Build from the repository root:

```sh
go build -o searchindex ./cmd/searchindex
```

Set `DATABASE_URL` to the canonical corpus DSN and `SEARCH_DATABASE_URL` to the independent index DSN. The account initializing the index needs permission to install `vector`; subsequent indexing needs write access to `public.semantic_chunks`. Keep both DSNs in private environment configuration.

```sh
./searchindex -schema-only
./searchindex -dry-run
./searchindex -bulk -workers 6 -batch 100 -group 500 -rate 1s
```

`-bulk` is useful for the first load of a new offline index. It creates storage, saves vectors, and builds an absent ANN index only after sync succeeds. It never drops an existing index, so an existing serving index continues to incur insertion maintenance. The operator can remove and rebuild an ANN index separately during an authorized offline load. `-schema-only` without `-bulk` can retry a failed ANN build without repeating embeddings. PostgreSQL session configuration controls index build memory.

Indexing requires `GEMINI_API_KEY` or `GOOGLE_API_KEY` in the environment. There is no API key flag. `-corpus-dsn` and `-index-dsn` override the DSN environment variables. `-model` defaults to `gemini-embedding-001`; each model has an independent set of vectors. Dry runs contact only the canonical corpus and print document/chunk counts without calling Gemini.

`-limit` bounds the number of documents and `-offset` skips documents in deterministic order. Batches contain up to 100 pending chunks across documents. `-group` controls documents queued per worker group, default 100 and maximum 1000. Larger groups, such as 500 during a bulk load, fill API batches more efficiently when documents produce uneven numbers of chunks. Memory grows with both worker count and group size, as each active or queued group holds its document text. `-workers` controls concurrent document groups; `-rate` spaces batch starts across all workers. Offline indexing retries a failed embedding batch at most three times. Interrupt with Ctrl+C and rerun the same command: completed chunks are reused, including chunks saved before an interrupted document finished. Old document generations remain until their replacement group finishes successfully. Indexing never globally deletes sources that were absent from an incomplete traversal. Only a successful unlimited traversal starting at offset zero prunes absent document generations. A separate document marker table tracks unchanged sources without updating their vectors. A session advisory lock serializes runs of the same model. `-progress 10000` prints cumulative counts after committed document groups cross each 10,000-chunk interval; zero disables progress output.

Chunks preserve exact source text with Unicode code point offsets, a maximum length of 900 and an overlap of 120. Each stores the SHA256 of the full canonical document text. Consumers must call `semantic.Load`, verify the canonical hash, and verify the exact contiguous substring before showing a hit. Pending, repealed, future-issued, future-effective or changed sources cannot be trusted merely because they remain in the index. Embedded topic explanations are available only while all their citations still match current eligible Arabic articles.

Run unit tests normally. For database integration, point `SEMANTIC_TEST_DSN` to a separate disposable pgvector database, never the serving index. Tests create and remove a corpus fixture schema, fixture index rows and a disposable database for the legacy-schema check; the test account therefore needs CREATEDB permission. `SEMANTIC_CORPUS_TEST_DSN` optionally enables a read-only comparison of bounded article rereads against a real canonical corpus view.

```sh
go test ./internal/semantic ./cmd/searchindex
```

Search uses a bounded iterative HNSW scan and an exact model-filtered fallback if ANN returns too few hits. This prevents older model namespaces from hiding the active model in a shared vector graph.

Existing `vector(768)` storage requires an explicit offline migration. Normal schema setup refuses the old type with migration guidance and never rewrites stored embeddings automatically. With indexing and serving stopped, back up the index database, drop the rebuildable ANN index, convert only its embedding column, and recreate ANN with the new operator class:

```sql
DROP INDEX IF EXISTS public.semantic_chunks_embedding;
ALTER TABLE public.semantic_chunks
  ALTER COLUMN embedding TYPE halfvec(768)
  USING embedding::halfvec(768);
CREATE INDEX semantic_chunks_embedding ON public.semantic_chunks
  USING hnsw (embedding halfvec_cosine_ops);
```

The conversion preserves source text, hashes, offsets, model namespaces and saved indexing progress. Allow temporary disk space for PostgreSQL's table rewrite and WAL, and configure build memory for the host before starting. Verify schema setup and retrieval after conversion before resuming serving. Use an index database backup to restore float32 storage if needed; converting half precision back to float32 cannot recover discarded precision.
