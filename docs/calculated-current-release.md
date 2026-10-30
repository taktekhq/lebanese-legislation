# Calculated current wording releases

The optional `data/article_calculated_current.parquet` contains five columns:
`law_id` int32, `number` string, `position` int32, `source_sha256` string and
`calculated_current` string. The last column is a JSON object imported as jsonb.
Only the canonical Go exporter generates calculations. The importer checks the
law, current article number and position, exact input text and SHA-256,
applicability date, state, pending flag and statutory authority. Invalid rows
abort the build, including duplicates. No arithmetic or spelling is implemented
in this repository.

Original `source_documents.text`, hashes, passage boundaries and vectors stay
unchanged. Retrieval adds `metadata.calculated_current`; literal substring
search also checks its `text_ar`. Use this object for current monetary amounts
and cite its authority. Label it as calculated wording, never a verbatim source
quotation. A matching passage's `text` remains the original source quotation.
Snapshots without the sidecar import an empty derived table and make no claims.
Previously published dumps remain restorable without this table.

## Reproducible full rebuild

Use a new empty PostgreSQL database and a separate output directory. Read the
old release only through `REUSE_DATABASE_URL`. Pin the dataset revision after
uploading the sidecar, rather than downloading from a moving branch.

```sh
python -m pip install '.[build,test]'
export DATASET_REVISION='<immutable 40-character Hub commit>'
export SNAPSHOT_DIR='./snapshot/data'
export RELEASE_ID='<unused release ID>'
hf download taktekhq/lebanese-legislation --repo-type dataset \
  --revision "$DATASET_REVISION" --include 'data/*.parquet' --local-dir ./snapshot
export DATABASE_URL='<new empty PostgreSQL staging database>'
export REUSE_DATABASE_URL='<read-only compatible released database>'
lebleg build --snapshot-dir "$SNAPSHOT_DIR" --revision "$DATASET_REVISION" \
  --as-of-date 2026-10-30 --release-id "$RELEASE_ID" \
  --output-dir "./artifacts/release-$RELEASE_ID"
```

The command reuses only matching canonical source references, source hashes,
exact excerpts and Unicode boundaries. It makes no embedding requests without
`--embed-missing`. Exit status 2 means incomplete coverage; inspect
`coverage.json` before considering paid embeddings. Preserve the staging DB and
use `--resume` with the identical identity/date to finish the same build.

Build and restore both require PostgreSQL 18.6, pgvector 0.8.2 and
`halfvec(768)`. The model is `gemini-embedding-001`, dimension 768,
`RETRIEVAL_DOCUMENT`, 900 character chunks and 120 character overlap.
Text search uses pg_trgm 1.6. Use matching PostgreSQL 18 client utilities.
The pinned database image is
`ghcr.io/taktekhq/lebanese-legislation-starter:postgres-18.6-vector0.8.2-r2`.
The previous image may have the same database contract; check its actual server
and extension versions. Build a runtime/publisher from this source commit so
retrieval includes calculated-text matching. An older runtime can restore the
additive table but its literal lookup cannot search the calculated wording.

Restore into a separate fresh database with the normal `lebleg restore` command
and versioned manifest. Verify 34 derived rows, article 86 metadata and literal
`5,000,000,000` lookup, original wording/hash parity, source/chunk integrity and
complete vector coverage. The dump includes the derived table automatically;
its count appears in `manifest.counts.tables`. Release format stays version 1.
Run independent review before upload or deployment. `lebleg publish` preserves
older release directories and refuses an existing release ID.

## Limited disk space

A full build needs space for both the new database and custom dump. The
2026-10-30.1 artifact is about 6 GB, excluding its database. Do not run a full
clone/build with only enough space for a second dump. Prefer another host or
volume. Never alter an in-use restore or build database.

A metadata-only staging update is possible when every original snapshot table
is identical to the prior pinned revision. It requires an independently owned
writable staging database, explicit coordination before reusing any existing
build database, and preservation of the old immutable artifact. A read-only
connection cannot produce a changed self-contained custom-format dump.

For such an owned staging database, create only the new table from
`sql/public_schema.sql`, import the pinned sidecar with its declared Arrow
schema and JSON decoding, then call `validate_calculated_rows(conn, as_of_date)`
and `refresh_calculated_metadata(conn, as_of_date)`. Verify all original source
and vector hashes remain identical. Rebuild the internal `lebleg_release`
identity, revision, generation time and table counts only after verifying parity
of all original 29 tables against the newly pinned dataset. Use
`build.validate_chunks` and `build.export_release` to validate and export to an
unused directory; the latter regenerates the external dump checksum/size.
This is an operator-managed staging optimization, not permission to patch a
published restored database. If any original table differs, use the full build.
