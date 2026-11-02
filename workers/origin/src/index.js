import { Container } from "@cloudflare/containers";

export class Origin extends Container {
  defaultPort = 8080;
  sleepAfter = "72h";

  constructor(ctx, env) {
    super(ctx, env);
    this.envVars = Object.fromEntries([
      "GEMINI_API_KEY", "POSTHOG_KEY", "LEBLEG_SEARCH_PROVIDER",
      "LEBLEG_VECTORIZE_INDEX", "CLOUDFLARE_ACCOUNT_ID", "LEBLEG_VECTORIZE_TOKEN",
      "LEBLEG_SEARCH_MODEL", "LEBLEG_SEARCH_SELECT_MODEL",
      "LEBLEG_SEARCH_DAILY_LIMIT", "LEBLEG_SEARCH_SHARES_DATA",
    ].filter((key) => env[key] !== undefined).map((key) => [key, env[key]]));
  }

  // A cold start opens a 10 GB database and warms the server's caches before it
  // listens, which takes longer than the library's default 20 seconds.
  async fetch(request) {
    // Also when the container is already running: it may still be opening
    // Postgres, and the library's own wait gives up after about 20 to 48 s.
    // Wait for the port once per start; checking it on every request added
    // about 1.5 s to each response.
    if (!this.portReady) {
      await this.startAndWaitForPorts({
        ports: [this.defaultPort],
        cancellationOptions: { instanceGetTimeoutMS: 120_000, portReadyTimeoutMS: 660_000 },
      });
      this.portReady = true;
    }
    try {
      return await super.fetch(request);
    } catch (err) {
      this.portReady = false;
      throw err;
    }
  }

  onStop() {
    this.portReady = false;
  }
}

export default {
  async fetch(request, env) {
    const origin = env.ORIGIN.get(env.ORIGIN.idFromName("main"), { locationHint: "weur" });
    const url = new URL(request.url);
    if (request.method === "GET" && url.pathname === "/_origin/log") {
      return origin.containerFetch("http://container/log", 8081);
    }
    if (request.method === "GET" && url.pathname === "/_origin/restart") {
      await origin.destroy();
      return new Response("container stopped\n");
    }
    return origin.fetch(request);
  },
};
