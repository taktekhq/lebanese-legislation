# Server

The Python package `lebleg`. One backend serves both public images:

- `ghcr.io/taktekhq/lebanese-legislation-api`: REST API with laws, articles, rulings, Gazette records and search.
- `ghcr.io/taktekhq/lebanese-legislation-mcp`: the same backend over MCP (`APP_MODE=mcp`).

The same package restores the database inside the [db image](../db/), and builds and publishes new dumps from the public Parquet files.

| Command | What it does |
| --- | --- |
| `lebleg serve` | Runs the API, or the MCP server with `APP_MODE=mcp` |
| `lebleg init` | Writes local passwords to `.env` (used by `./start`) |
| `lebleg search` | Searches a restored database from the command line |
| `lebleg restore` | Restores a published dump into your own PostgreSQL |
| `lebleg build` | Builds a new dump from pinned Parquet files, reusing saved vectors |
| `lebleg publish` | Uploads a verified dump to a Hugging Face dataset |
| `lebleg seed` | Copies compatible vectors into another index |
| `lebleg pipeline` | The resumable data update pipeline, see [docs/pipeline.md](../docs/pipeline.md) |

Build the images from the repository root:

```sh
docker build -f server/Dockerfile --target api -t lebanese-legislation-api .
docker build -f server/Dockerfile --target mcp -t lebanese-legislation-mcp .
```

Develop and test from this folder:

```sh
pip install '.[build,test]'
pytest -q
ruff check src tests
```

Tests that need PostgreSQL or Docker images skip unless you set their variables. The [checks workflow](../.github/workflows/ci.yml) shows a full disposable setup. Everything else is in the [service reference](../docs/reference.md).
