# MCP on Cloudflare

Part of [LebaneseLegislation.com](https://lebaneselegislation.com/). See the [project README](../../README.md) for the local services, the [database image](https://github.com/orgs/taktekhq/packages/container/package/lebanese-legislation-db) and the [Parquet dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation).

This is a standalone MCP deployment example. The production website container
is described in [architecture](../../docs/architecture.md).

This Worker starts the MCP service in [Cloudflare Containers](https://developers.cloudflare.com/containers/). Containers require a Workers Paid plan. Docker images run in Containers; native Workers route requests to them.

Use Node 22 or newer, Docker, and your own Cloudflare account:

```sh
npm ci
npx wrangler login
npx wrangler secret put DATABASE_URL
npm run check
npm run deploy
```

Supply a compatible, network-accessible PostgreSQL database with TLS. Use a read-only database role. The [DB image](../../db/) supplies the database. Nothing here publishes a database or its credentials.

Connect clients to the returned Worker URL at `/mcp`. `/healthz` checks process readiness. The container sleeps after 60 seconds idle; the first request can take longer. One named container serves all requests. This example has no authentication or rate limiting; add those at the edge for a private or busy deployment.

The container uses this project's versioned MCP image. `DATABASE_URL` is a Worker secret passed at process startup. After changing it, restart or redeploy the container. `PUBLIC_URL` is optional for canonical links. No account IDs or production endpoints are included.

```sh
npm test
```

Configuration follows Cloudflare's [runtime environment example](https://developers.cloudflare.com/containers/examples/env-vars-and-secrets/). A dry run validates the Worker bundle and configuration, not remote database connectivity or Cloudflare provisioning.

For browser MCP clients, set `MCP_ALLOWED_ORIGINS` to a comma-separated list of exact allowed origins. Clients without an Origin header need no extra setting.

Optional semantic search uses your own `GEMINI_API_KEY` Worker secret. Query budgets are per process and reset when a container restarts; use provider billing limits for a durable cap.

## Code and datasets

The former private `taktekhq/lebaneselegislation` code, scraper, build pipeline,
website and MCP are now in [this public repository](https://github.com/taktekhq/lebanese-legislation).
See [architecture](../../docs/architecture.md) for the website's Cloudflare
Worker, R2 and container setup. Hosted semantic search uses Vectorize index
`lebleg-chunks`; the vectors are openly available as
[`vectors/vectors.parquet`](https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet). See the
[DuckDB example](../../docs/reference.md#vectors).
