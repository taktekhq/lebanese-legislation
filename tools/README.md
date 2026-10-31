# Data tools

The Python package `lebleg`, for the database itself. The services ([`web/`](../web/), [`api/`](../api/), [`mcp/`](../mcp/)) are Go; this package restores, rebuilds and publishes the data they read.

| Command | What it does |
| --- | --- |
| `lebleg restore` | Restores a published dump into PostgreSQL, checks it and adds what the services read ([`sql/web.sql`](src/lebleg/sql/web.sql)). The [db image](../db/) runs it on first start |
| `lebleg build` | Builds a new dump from pinned Parquet files, reusing saved vectors |
| `lebleg publish` | Uploads a verified dump to a Hugging Face dataset |
| `lebleg seed` | Copies compatible vectors into another index |
| `lebleg pipeline` | The resumable data update pipeline, see [docs/pipeline.md](../docs/pipeline.md) |

Develop and test from this folder:

```sh
pip install '.[build,test]'
pytest -q
ruff check src tests
```

Tests that need PostgreSQL or Docker images skip unless you set their variables. The [checks workflow](../.github/workflows/ci.yml) shows a full disposable setup, including the four images in Compose. Everything else is in the [service reference](../docs/reference.md).
