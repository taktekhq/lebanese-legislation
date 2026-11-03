# Contribute

Improve the website, search, API, MCP or public data tools here. For search or legal-text issues, include a public source URL, the query and the expected result. Keep examples free of personal details.

Describe the change and the checks you ran:

```sh
# Go: internal/, cmd/ and the web/, api/ and mcp/ services
go vet ./... && go test ./...
# The release commands against a throwaway pgvector PostgreSQL (see .github/workflows/checks.yml)
docker build -f db/Dockerfile --target postgres -t lebleg-test-postgres .
LEBLEG_RELEASE_TEST_DSN=postgres://... go test ./internal/releasedb/
# Cloudflare Workers
node --test workers/static-site/test/*.test.mjs workers/origin/test/*.test.mjs
```

New services and tools are written in Go. The [website guide](web/README.md) includes a fictional database fixture. The [service reference](docs/reference.md#rebuild-the-release) explains rebuilding the release and publishing forks. Reuse compatible vectors; new paid embeddings need explicit flags and a budget. All four images publish from this repository with immutable version tags.

Keep secrets, private documents, queries, infrastructure and source collection machinery out of contributions. Report sensitive issues privately to nizar@taktek.io. MIT covers code; preserve document rights and attribution.
