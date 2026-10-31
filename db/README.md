# Database image

`ghcr.io/taktekhq/lebanese-legislation-db` is PostgreSQL 18.6 with pgvector 0.8.2, built from the official `postgres:18.6` image. Both are pinned: the base by digest, the pgvector source by SHA-256.

On first start with an empty volume it reads `releases/latest.json` from the [Hugging Face dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation/tree/main/releases), downloads that dump, checks its SHA-256 and restores it. The container reports healthy only after the restore passes its checks. Later starts reuse the volume and download nothing.

| File | Role |
| --- | --- |
| `Dockerfile` | Targets `postgres` (pgvector only), `db` (the published image) and `publisher` (snapshot tools) |
| `init-reader.sh` | Creates the read-only `lebleg_reader` role when `LEBLEG_READER_PASSWORD` is set |
| `init-release.sh` | Runs the restore once, when PostgreSQL initialises an empty volume. The restore also adds what the website, API and MCP read (`tools/src/lebleg/sql/web.sql`) |
| `release.py` | Downloads, verifies and restores the release, using the [`tools/`](../tools/) package |
| `health.sh` | Healthy only when PostgreSQL answers over TCP, the restore receipt is valid and `web.sql` has run |
| `web-schema.sh` | Adds those pieces to a database restored by 0.2.0 or older |

Build from the repository root:

```sh
docker build -f db/Dockerfile -t lebanese-legislation-db .
```

Settings, pinning a release and retrying a failed restore are in the [service reference](../docs/reference.md).
