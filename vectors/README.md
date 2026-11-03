# Vectors image

`ghcr.io/taktekhq/lebanese-legislation-vectors:latest` contains the
`vectorize-sync` and `export-vectors` binaries and DuckDB 1.5.6. It has no
embedded dataset. Its default command downloads `vectors/vectors.parquet` from
Hugging Face and syncs the index configured through environment variables.
Build with `docker build -f vectors/Dockerfile -t lebleg-vectors .`.

## Load your own Vectorize index

Create a 768-dimensional cosine index and a token with Vectorize edit access.
Set your own values, then run:

```sh
export CLOUDFLARE_ACCOUNT_ID=your-account
export LEBLEG_VECTORIZE_TOKEN=your-token
export LEBLEG_VECTORIZE_INDEX=your-index
mkdir -p vector-state
docker run --rm -v "$PWD/vector-state:/work" \
  -e CLOUDFLARE_ACCOUNT_ID -e LEBLEG_VECTORIZE_TOKEN -e LEBLEG_VECTORIZE_INDEX \
  ghcr.io/taktekhq/lebanese-legislation-vectors:latest
```

Keep `/work/vectors-manifest.tsv` between runs. Unchanged hashes are skipped;
IDs in the previous manifest but absent from the input are deleted. A first
load cannot discover unrelated IDs already in your index. Use an empty index
or supply its previous manifest. `--dry-run` prints counts without changing it.
Override `VECTORS_URL` for another public Parquet URL, or mount a local file and
set `VECTORS_INPUT=/work/current.parquet`. `VECTORS_PREVIOUS` and
`VECTORS_MANIFEST` override the manifest paths. Use `--entrypoint export-vectors`
to export a pgvector database with `LEBLEG_VECTOR_DSN` and `-out /work/vectors.ndjson`.

The repository workflow generates the current IDs from `data/`, filters the
published Parquet to them and embeds missing chunks with Gemini. It preserves the
archive's ID list alongside the previous sync manifest as the deletion baseline.
This removes the 21 stale archive IDs, even if the previous manifest is absent.
The workflow runs this image with the filtered artifact and only publishes it
after sync succeeds, so a failed sync cannot lose the archive deletion baseline.

Missing chunks are embedded with Gemini under a $1 daily budget in Asia/Beirut,
with a cap of 20,000 new chunks per run. Completed rows and the daily budget
are cached between runs; if coverage is incomplete, publication stops and the
next run continues from those rows.

## Load pgvector

Start the `-db` image and download the public Parquet. The database already
contains matching chunk text and metadata. With DuckDB 1.5.6 and `psql` installed:

```sh
curl -fL -o vectors.parquet https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet
duckdb -c "COPY (SELECT id, '[' || array_to_string(values, ',') || ']' AS embedding FROM read_parquet('vectors.parquet')) TO 'embeddings.csv' (FORMAT CSV);"
export PGDATABASE=lebleg_search
# Also set PGHOST, PGPORT, PGUSER and PGPASSWORD for your own database.
psql -v ON_ERROR_STOP=1 <<'SQL'
CREATE EXTENSION IF NOT EXISTS vector;
ALTER TABLE semantic_chunks ADD COLUMN IF NOT EXISTS embedding halfvec(768);
CREATE TEMP TABLE imported_vectors (id text PRIMARY KEY, embedding halfvec(768));
\copy imported_vectors FROM 'embeddings.csv' WITH (FORMAT CSV, HEADER)
WITH chunks AS (
  SELECT ctid, model || ':' || kind || ':' || source_id::text || ':' || part || ':' || lang || ':' || hash || ':' || chunk_start::text AS raw
  FROM semantic_chunks
)
UPDATE semantic_chunks c SET embedding=v.embedding
FROM chunks r JOIN imported_vectors v ON v.id=CASE
  WHEN octet_length(r.raw)<=64 THEN r.raw
  ELSE encode(substring(sha256(convert_to(r.raw,'UTF8')) from 1 for 16),'hex') END
WHERE c.ctid=r.ctid;
-- Refuse incomplete coverage before building the index.
ALTER TABLE semantic_chunks ALTER COLUMN embedding SET NOT NULL;
CREATE INDEX IF NOT EXISTS semantic_chunks_embedding ON semantic_chunks USING hnsw (embedding halfvec_cosine_ops);
ANALYZE semantic_chunks;
SQL
```

Use the model `gemini-embedding-001` for queries. Point `LEBLEG_VECTOR_DSN` at
`lebleg_search`, supply `GEMINI_API_KEY`, and leave `LEBLEG_VECTORIZE_INDEX`
unset to use pgvector. Importing vectors changes your local volume only.
