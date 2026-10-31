# Automatic data updates

Install the publisher from this repository with `pip install './server[build]'`. The pipeline reuses the existing builder, embeddings and restore checks. Serving images need no data-only rebuild.

Copy [the example config](../server/pipeline.example.json) outside Git. Set your dataset, private runtime paths and a dedicated PostgreSQL staging administrator. Provision restricted secret files and a `0600` environment file containing `PIPELINE_ADMIN_DSN`, `PIPELINE_REUSE_DSN` and `PIPELINE_READER_PASSWORD`. Never commit them. The staging cluster must already have the restricted `lebleg_reader` login.

```sh
lebleg pipeline --config /path/pipeline.json seed --repo owner/dataset --revision SOURCE_COMMIT --release-id EXISTING_RELEASE
lebleg pipeline --config /path/pipeline.json detect
lebleg pipeline --config /path/pipeline.json status
```

Seed only an existing verified release. Detection hashes data, schema, scan shards and annotation files. README and release-pointer changes do not create another build. A new corpus revision stays in reconciliation until the private canonical exporter proves and approves its exact content fingerprint:

```sh
lebleg pipeline --config /path/pipeline.json approve --repo owner/dataset --revision SOURCE_COMMIT --fingerprint CONTENT_SHA256
lebleg pipeline --config /path/pipeline.json run
```

Schedule `tick` every five minutes, or run it after a successful export. A lock prevents overlapping workers. Interrupted stages resume from their receipts. Failed attempts back off; `retry --job ID` retries a held job. `status` reports separate first-party freshness lanes rather than treating a published snapshot as proof that a running site was refreshed.

Document embedding attempts share a durable daily reservation ledger, including failed attempts. The default is $1/day in Beirut time using the configured [Gemini embedding rate](https://ai.google.dev/gemini-api/docs/pricing). This is a conservative local token guard, not an invoice or an account-wide provider spending limit. Existing services outside this coordinator retain their own limits. Set `PIPELINE_SMOKE_SEMANTIC=1` in the runtime environment to require a live semantic query before promotion. Smoke queries have a separate maximum of three attempts per release.

The worker imports pinned Parquet, reuses unchanged vectors and indexes changed text. It validates the staged database, exports a dump, restores it separately, uploads an immutable candidate, checks anonymous downloads and runs the actual API/MCP images. Only then can it promote `latest`. A conflicting newer pointer is left untouched.

Runtime files stay under the private state directory. Managed dataset-card blocks preserve YAML and attribution. An optional card-block file lets a canonical exporter keep those release links. Configure collection updates only with appropriate token permissions; errors remain visible. Scan updates verify their content manifest and counts without inventing transcriptions or importing unreviewed text.

Retention removes only marked pipeline-owned scratch databases and workspaces, keeping the current and previous successful staging builds. It never replaces users' existing volumes. Allow at least 40 GB of free workspace; larger datasets need more.

If PostgreSQL 18.6 clients are unavailable, link [pg-client.sh](../server/scripts/pg-client.sh) as `pg_dump` and `pg_restore`, set their paths in the config and set `PG_CLIENT_MOUNT` to the state directory. The helper uses the DB image's clients, with credentials passed through environment variables.
