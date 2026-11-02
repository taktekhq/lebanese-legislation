# Calculated current wording

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

## Get the calculated wording

Use the [`-db` or `-all` image](../README.md#images) for PostgreSQL, or the
[Parquet tables on Hugging Face](https://huggingface.co/datasets/taktekhq/lebanese-legislation) for portable data.
The images build both databases from the checked-in `data/` tables, including
`article_calculated_current.jsonl`. See [the database image](../db/README.md)
and [service reference](reference.md) for setup.

Verify the derived rows, article 86 metadata and literal `5,000,000,000` lookup,
and original wording and hash parity when rebuilding the data.
