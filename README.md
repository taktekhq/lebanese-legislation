# Lebanese Legislation services

Run a local Lebanese legal database, HTTP API and MCP server from the public [dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation). All three service images support Linux AMD64 and ARM64.

| Image | Purpose |
| --- | --- |
| `ghcr.io/taktekhq/lebanese-legislation-db:0.1.6` | PostgreSQL 18.6, pgvector 0.8.2 and verified first-run restore |
| `ghcr.io/taktekhq/lebanese-legislation-api:0.1.6` | General resources and keyword, citation and optional semantic search |
| `ghcr.io/taktekhq/lebanese-legislation-mcp:0.1.6` | The same retrieval and resources as MCP tools |

The [database releases](https://huggingface.co/datasets/taktekhq/lebanese-legislation/tree/main/releases) include source records, indexed passages and optional calculated-current metadata. Default startup downloads the latest verified snapshot.

## DB only

Use the DB image as a normal Compose service. Set your own password:

```yaml
services:
  db:
    image: ghcr.io/taktekhq/lebanese-legislation-db:0.1.6
    environment:
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?Set a password}
    volumes:
      - legislation:/var/lib/postgresql
volumes:
  legislation:
```

Set `LEBLEG_READER_PASSWORD` to also create the `lebleg_reader` login. No separate restore container or CLI is required.

## Full setup

Install Docker with Compose. Allow 4 GB of available memory and 30 GB of disk space; 8 GB of memory is recommended.

```sh
git clone https://github.com/taktekhq/lebanese-legislation-starter.git
cd lebanese-legislation-starter
./start
```

The helper generates local credentials. The DB image downloads, checks and restores the selected snapshot on an empty volume. API and MCP wait for its verified receipt. Volume refresh is yours to manage.

API documentation: http://localhost:8000/docs. MCP: http://localhost:8001/mcp. Published ports bind to localhost. Add HTTPS and access controls before public hosting.

## Use

`/api/v1/` serves laws, articles, rulings and Gazette records. `/api/v1/search/` retains the grouped resource response, with exact search by default; `semantic=true` adds related passages. `/search` and `/api/search` return source passages with optional semantic retrieval. MCP calls the same implementation. Original text, source links and uncertainty travel with the result.

Exact search needs no AI key. Optional semantic search uses `GEMINI_API_KEY` to embed the query against saved vectors. Provider budgets apply per API/MCP process. Source passages are not legal advice. Check original publications before relying on machine readings or recorded legal status.

Calculated current monetary wording, when exported by the canonical dataset, is available as `source_documents.metadata.calculated_current` and the `article_calculated_current` table. Literal search also matches calculated text and amounts. Returned passage text stays verbatim. Use the labelled object for current monetary answers and cite its authority. Older snapshots without the optional sidecar make no calculated claims. See [the release and compatibility guide](docs/calculated-current-release.md).

[API and deployment reference](docs/reference.md) · [Contributor tools](CONTRIBUTING.md) · [Website source](https://github.com/taktekhq/lebanese-legislation)

The website stays buildable from source for offline hosting. It is not a published service image. Existing image tags remain available. Code is MIT licensed; source documents retain their own rights.

Data publishers can use the [automatic update pipeline](docs/pipeline.md) to reconcile approved changes and publish verified snapshots.
