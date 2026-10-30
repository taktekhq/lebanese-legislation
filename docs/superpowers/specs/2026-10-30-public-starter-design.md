# Public PostgreSQL and pgvector starter

## Approved purpose

Keep the existing Hugging Face Parquet dataset. Publish a restorable public PostgreSQL snapshot with saved pgvector embeddings, a public Docker image and a reusable open-source starter. Other developers and our own team should use the same release artifacts. Nizar approved this package in chat on 30 October 2026 after the Parquet/dump/GitHub architecture was explained.

## Decisions and scope

- Public MIT code repository: taktekhq/lebanese-legislation-starter. Copyright Taktek and Nizar Mahmoud. This is a new repository; the existing private website repository stays private.
- Hugging Face dataset: taktekhq/lebanese-legislation. Existing 29 Parquet files stay intact. New immutable release folders contain a public custom-format PostgreSQL dump and manifest. A latest pointer selects a release. All downloads pin a resolved HF commit before fetching files.
- The dump is built exclusively from the published Parquet snapshot, never from a whole private/production database. Published views are restored as snapshot tables. Preserve public column values/types, identifiers and applicable public keys/relations; do not invent missing source records.
- Preserve the dataset's existing attribution/licence terms separately from the code's MIT licence. English titles remain labelled unofficial discovery aids, Arabic authoritative; French source text retained. Machine-read content retains its source/confidence metadata. No new translations or legal wording generation.
- PostgreSQL18.6 plus pgvector0.8.2, pinned base and extension checksum, portable compilation. 768-dimensional Gemini embedding-001 vectors, halfvec. Chunks900Unicode code points, overlap120, matching the deployed site's existing chunks.
- Reuse saved vectors only after reference, full-document SHA256, offsets and exact substring match against the public snapshot. Website-only explanations lacking published dataset source material are excluded. Source metadata/title comes from the public snapshot. Missing vectors are counted before any paid calls; optional explicitly requested embedding fill has a configurable cap. No private search queries, keys, passwords, private provenance fields or run history in public artifacts.
- Public starter provides restore/download, a small retrieval CLI/API and optional source-ID-only quotation selection. It is a developer starter, not a copy of the private website. No model training or Hugging Face model weights in this release.
- Our website retains its current exact-first UI and canonical checks. It can reuse the public compatible image and semantic table format. Publication does not require swapping its canonical database or deploying unrelated changes.

## Data contract

Public corpus tables are the existing Parquet tables, with typed arrays, dates, booleans, numerics and text preserved. Exported views are explicitly materialized snapshot tables, not the private editorial/build pipeline.

`source_documents`: kind text, source_id integer, part text, lang text, title text, text text, hash text, source_url text, metadata jsonb; primary key(kind,source_id,part,lang). This is the normalized authoritative read model of the published snapshot. Metadata includes source type, identifier/date where available, language and English-aid status. Snapshots are read-only; a source update requires rebuilding the consistent release. Query-time hash/substring validation excludes stale chunks if this read model changes.

`semantic_chunks` uses the deployed format: model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding halfvec(768),updated_at. Primary key(model,kind,source_id,part,lang,hash,chunk_start), source-reference index and HNSW halfvec_cosine_ops. Source hashes and offsets are canonical Unicode code point offsets. `semantic_documents` retains the compatible reference/hash/seen_run format for resumable refresh; release values contain only public build metadata.

`lebleg_release` stores the release identity/source revision/model/counts. External manifest contains format_version1,release_id,generated_at,source repository and immutable revision, PostgreSQL/extension requirements, embedding model/dimensions/task/chunk specification, source table/document/chunk counts, dataset licence/credit, build version and dump path/size/SHA256. The dump cannot contain its own checksum; restore receipt records the checked external manifest after import.

## Consumer flow

A start script generates local random passwords into ignored0600.env when absent, then Docker Compose starts database, one-shot verified restore and a read-only API. Published image contains PostgreSQL/pgvector, matching pg_dump/pg_restore and a small Python runtime. Local builds remain available. Only loopback ports are published by default. API receives a read-only database credential and an optional user-owned Gemini key. Owner credentials belong only to DB/bootstrap. No provider key is needed to download/restore or use ordinary lookup. Semantic querying requires the same embedding model used in the snapshot; incompatible model/dimension is rejected. Keys remain server-side; redirects/provider errors cannot expose them.

Restore validates a versioned manifest and streaming checksum, refuses an occupied/unrelated database or mismatched existing release, restores transactionally, analyzes tables, checks counts/schema/indexes/roles, then records readiness. Rerunning the same release is idempotent. Existing data is never automatically dropped. Upgrades use a new volume/database and an explicit documented switch.

Retrieval returns exact source matches first, then semantic passages with original text and source links. Typed explicit citations are not silently replaced by different identifiers/dates. Results are passages, not legal conclusions. Optional Gemini selection returns supplied IDs only; server owns copied text/links and rejects invented IDs/wording. Models get no DB credentials or external tools.

## Publisher flow

Resolve a published Parquet commit, download its29files, build a fresh isolated public database, derive eligible source documents as of the snapshot, and import reusable vectors through a read-only source connection. Validate all retained chunks against public sources. Report missing coverage, optionally embed only missing chunks within an explicit cap, then build indexes and validate full coverage. Export custom PostgreSQL dump without roles, owners or ACLs. Restore it into another empty database using the public image and exercise source/semantic query/reader permissions before publication.

Publish immutable artifacts and a latest pointer to HF only after verification. Keep Parquet files unchanged; update the dataset card with download/restore links and dependency/model requirements. Put code, Dockerfiles, tests, instructions and image build workflow on GitHub. Publish a versioned public container and release. No raw production dump or private dataset is published.

## Validation and adoption

Tests cover manifest/path/checksum failures, nonempty target refusal, interrupted restore, same-release rerun, type preservation, source eligibility, Unicode chunks, stale hash/offset rejection, model mismatch, key redaction, exact citation boundaries and read-only access. API/provider tests use mocks, with a small actual-key retrieval smoke only after full public restore. Real full artifact restore checks published table counts and corpus/index referential coverage. GitHub CI and independent implementation review precede release.

Document CPU/RAM/disk/download requirements honestly: saved embeddings avoid paid document re-embedding, but pg_restore still rebuilds HNSW and takes time. Use the public image locally in our build/restore path, and integrate reusable public tooling or image verification in the private publication workflow without changing current Parquet output. All temporary services and transfer files are cleaned up; source code, release receipts and lessons are committed/pushed.

## Builder-owned calls

Python3.11+, psycopg3 and a small FastAPI/uvicorn service; PyArrow and HF SDK only for the publisher extra. Keep modules separate for manifest/download, restore, source/schema import, vector reuse/fill and query/provider. Set sensible bounded pools, provider timeouts and caches. Choose public synthetic test fixtures and suitable PK/FK metadata from the existing public schema. Settle small version/tag/CLI names in the README. A data/schema mismatch fails publication rather than silently changing public source text.
