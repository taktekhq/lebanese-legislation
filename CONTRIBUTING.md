# Contribute

Improve the website, search, API, MCP or public data tools here. For search or legal-text issues, include a public source URL, the query and the expected result. Keep examples free of personal details.

Describe the change and the checks you ran:

```sh
pip install '.[build,test]'
pytest -q
ruff check src tests
go test ./...
node --test examples/handwriting/test.mjs cloudflare/runtime.test.mjs
```

The [website guide](docs/website.md) includes a fictional database fixture. The [service reference](docs/reference.md#rebuild-from-public-parquet) explains snapshot reproduction and publishing forks. Reuse compatible vectors; new paid embeddings need explicit flags and a budget. All three service images publish from this repository with immutable version tags.

The [handwriting demo](https://huggingface.co/spaces/taktekhq/lebanese-legal-handwriting) saves local drafts for review. It has no sign-up or live submission flow.

Keep secrets, private documents, queries, infrastructure and source collection machinery out of contributions. Report sensitive issues privately to taktekbot@taktek.io. MIT covers code; preserve document rights and attribution.
