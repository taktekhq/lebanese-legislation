# MCP on Cloudflare

Part of [LebaneseLegislation.com](https://lebaneselegislation.com/). See the [project README](../../README.md) for the local services, the [PostgreSQL dump](https://huggingface.co/datasets/taktekhq/lebanese-legislation/tree/main/releases) and the [Parquet dataset](https://huggingface.co/datasets/taktekhq/lebanese-legislation).

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

The MCP server answers browser clients from any origin, as the public endpoint does. Restrict origins at the edge if you need to.

Optional semantic search uses your own `GEMINI_API_KEY` Worker secret. Query budgets are per process and reset when a container restarts; use provider billing limits for a durable cap.
