# Lebanese Legislation

The code and data behind [LebaneseLegislation.com](https://lebaneselegislation.com/): 103,118 laws, 389,078 articles, 47,657 court rulings and 6,851 Official Gazette issues, with search, a REST API and an MCP server.

## Where everything is

| You want | Go to |
| --- | --- |
| Read the laws | [LebaneseLegislation.com](https://lebaneselegislation.com/) |
| Connect an AI assistant | The free MCP server at `https://lebaneselegislation.com/mcp`. [Setup](docs/mcp.md) |
| The data as tables | [Parquet files on Hugging Face](https://huggingface.co/datasets/taktekhq/lebanese-legislation) |
| The data as a PostgreSQL database | The [6.4 GB dump on Hugging Face](https://huggingface.co/datasets/taktekhq/lebanese-legislation/tree/main/releases), with search vectors. The `-db` image below loads it for you. |
| The original court scans | [Court scans on Hugging Face](https://huggingface.co/datasets/taktekhq/lebanese-court-scans) |
| Everything on your own machine | [Run the services locally](#run-the-services-locally) |

Data lives on Hugging Face. Code and Docker images live here. Each GitHub release is a code version, never a data download.

## Run the services locally

```sh
git clone https://github.com/taktekhq/lebanese-legislation.git
cd lebanese-legislation
./start
```

You need Docker Compose, 4 GB of free memory and 30 GB of free disk. The first start downloads the latest dump from Hugging Face, checks its SHA-256 and restores it. That can take up to two hours. Later starts reuse the volume.

Then open the website at http://localhost:8080, the API at http://localhost:8000/docs, or point an MCP client at http://localhost:8001/mcp. Exact and citation search need no AI key. For semantic search, add your own `GEMINI_API_KEY` to `.env`.

| Image | What it is |
| --- | --- |
| [`ghcr.io/taktekhq/lebanese-legislation-db:0.3.0`](https://github.com/orgs/taktekhq/packages/container/package/lebanese-legislation-db) | PostgreSQL 18.6 with pgvector. Restores the dump into an empty volume on first start. |
| [`ghcr.io/taktekhq/lebanese-legislation-api:0.3.0`](https://github.com/orgs/taktekhq/packages/container/package/lebanese-legislation-api) | REST API: laws, articles, rulings, Gazette, exact and semantic search. |
| [`ghcr.io/taktekhq/lebanese-legislation-mcp:0.3.0`](https://github.com/orgs/taktekhq/packages/container/package/lebanese-legislation-mcp) | The same backend as the API, served over MCP. |
| [`ghcr.io/taktekhq/lebanese-legislation-web:0.3.0`](https://github.com/orgs/taktekhq/packages/container/package/lebanese-legislation-web) | The website, in Arabic and English, with keyword search. |

All four run on Linux AMD64 and ARM64. Nothing is baked into the images: the data comes from Hugging Face.

```
Hugging Face releases/latest.json --> db  (restores once into a volume)
                                      ^
                       web :8080 -----+
                       api :8000 -----+
                       mcp :8001 -----+
```

[Service reference](docs/reference.md): configuration, using your own PostgreSQL, rebuilding the dump.

## What is in this repository

| Folder | What it is |
| --- | --- |
| [`db/`](db/) | The PostgreSQL image. Builds pgvector from source on the official `postgres:18.6` image. |
| [`server/`](server/) | The Python package `lebleg`: one backend for the API and the MCP server, plus the restore, build and publish tools. |
| [`web/`](web/) | The Go website, published as the `-web` image. |
| [`examples/`](examples/) | The MCP image on Cloudflare Containers, and the [handwriting demo](https://huggingface.co/spaces/taktekhq/lebanese-legal-handwriting). |
| [`docs/`](docs/) | Service reference, MCP setup and the data update pipeline. |

`compose.yaml` runs the four images. `compose.build.yaml` builds them from this checkout instead. `compose.local.yaml` loads a dump you already downloaded. `server.json` is the [MCP Registry](https://registry.modelcontextprotocol.io/) entry and `.mcp.json` the client configuration for the hosted server.

Collecting the source texts stays in a separate private repository.

## Contribute and licence

See [CONTRIBUTING.md](CONTRIBUTING.md).

Code is [MIT licensed](LICENSE), by Taktek and Nizar Mahmoud. The data has its own [terms and attribution](https://huggingface.co/datasets/taktekhq/lebanese-legislation#what-to-check): credit the Centre for Research and Studies in Legal Informatics, Lebanese University, for the texts. The code licence does not cover the legal texts or court scans. This is not legal advice or an official publication.
