# Contribute

Improve the website, search, API, MCP or public data tools here. For search or legal-text issues, include a public source URL, the query and the expected result. Keep examples free of personal details.

Describe the change and the checks you ran:

```sh
# Go: internal/ and the web/, api/ and mcp/ services
go vet ./... && go test ./...
# tools/: restore, build, publish and the update pipeline
cd tools && pip install '.[build,test]' && pytest -q && ruff check src tests && cd ..
# mcp/cloudflare/
node --test mcp/cloudflare/runtime.test.mjs
```

New services and tools are written in Go. The [website guide](web/README.md) includes a fictional database fixture. The [service reference](docs/reference.md#rebuild-from-public-parquet) explains snapshot reproduction and publishing forks. Reuse compatible vectors; new paid embeddings need explicit flags and a budget. All four images publish from this repository with immutable version tags.

Keep secrets, private documents, queries, infrastructure and source collection machinery out of contributions. Report sensitive issues privately to taktekbot@taktek.io. MIT covers code; preserve document rights and attribution.
