# Contribute

Help improve retrieval, fix metadata handling, add compatibility tests or reproduce a public snapshot. Include a public source link and a small example for search or legal-text issues. Send sensitive reports privately to taktekbot@taktek.io.

Publisher tools build from the public Parquet dataset. They let contributors reproduce releases and publish forks. They do not include private collection or scraping code.

```sh
pip install '.[build,test]'
pytest -q
ruff check src tests
docker build --target publisher -t lebleg-publisher .
```

Use your own empty build database and publishing credentials. Reuse existing compatible vectors; new paid embeddings require explicit flags and a budget. Snapshot generation, validation and publication commands are in the [reference](docs/reference.md#rebuild-from-public-parquet).

Public service releases use immutable version tags. DB/API builds live here; the MCP image is a thin wrapper around the matching API image in the [website repository](https://github.com/taktekhq/lebanese-legislation). Both transports use this same source backend.

Keep secrets, private queries, infrastructure details and source collection machinery out of contributions. MIT covers code only; preserve document attribution and rights.
