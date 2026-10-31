# Lebanese Legislation

The website, PostgreSQL database, API, MCP server and public-data tools in one repository.

Read the [website](https://lebaneselegislation.com), use the [datasets](https://huggingface.co/collections/taktekhq/lebanese-legislation), or run the services locally.

## Choose a resource

- [Browse LebaneseLegislation.com](https://lebaneselegislation.com/): the public legal library.
- [Download PostgreSQL dataset 2026-10-30.2](https://github.com/taktekhq/lebanese-legislation/releases/tag/data-2026-10-30.2): a GitHub release record with the manifest and checksums. The 6.4 GB dump is hosted on Hugging Face and linked from that release.
- [Use the Parquet tables](https://huggingface.co/datasets/taktekhq/lebanese-legislation): portable structured data, schema and provenance.
- [Inspect court scans](https://huggingface.co/datasets/taktekhq/lebanese-court-scans): original PDFs and page indexes, with separate source rights.
- [Explore the dataset collection](https://huggingface.co/collections/taktekhq/lebanese-legislation): related datasets and the local handwriting demo.

## Run the services locally

```sh
git clone https://github.com/taktekhq/lebanese-legislation.git
cd lebanese-legislation
./start
```

Requires Docker Compose, 4 GB of available memory and 30 GB of free disk. First startup downloads and verifies the public snapshot, then restores it into an empty volume. Existing volumes are reused.

| Image | Purpose |
| --- | --- |
| [`ghcr.io/taktekhq/lebanese-legislation-db:0.1.7`](https://github.com/orgs/taktekhq/packages/container/package/lebanese-legislation-db) | PostgreSQL 18.6, pgvector and first-start restore |
| [`ghcr.io/taktekhq/lebanese-legislation-api:0.1.7`](https://github.com/orgs/taktekhq/packages/container/package/lebanese-legislation-api) | General resources, exact search and optional semantic search |
| [`ghcr.io/taktekhq/lebanese-legislation-mcp:0.1.7`](https://github.com/orgs/taktekhq/packages/container/package/lebanese-legislation-mcp) | The same retrieval backend through MCP |

API and docs: http://localhost:8000/docs. MCP: http://localhost:8001/mcp. Exact search needs no AI key. Semantic search uses your own `GEMINI_API_KEY` against saved embeddings.

[Service reference](docs/reference.md) · [Build the website](docs/website.md) · [Cloudflare](cloudflare/README.md) · [Contribute](CONTRIBUTING.md)

The website stays buildable from source for local or offline hosting. Try the [handwriting demo](https://huggingface.co/spaces/taktekhq/lebanese-legal-handwriting) or its [source](examples/handwriting/README.md). Drafts stay local.

Contributor tools reproduce snapshots from public Parquet. See [data updates](docs/pipeline.md) for the resumable release pipeline. Source collection and private operations stay separate.

Code is [MIT licensed](LICENSE). The dataset has separate [source rights and attribution](https://huggingface.co/datasets/taktekhq/lebanese-legislation#what-to-check); the code licence does not relicense the legal texts or court scans. Previous image tags and dataset releases remain available.
