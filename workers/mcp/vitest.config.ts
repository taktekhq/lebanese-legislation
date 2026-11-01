import { defineWorkersConfig } from "@cloudflare/vitest-pool-workers/config";

// Runs the Worker's own code against a local, in-memory D1 (no network,
// no real Cloudflare account touched) seeded by test/fixture.sql: a tiny
// slice of the real schema with a handful of hand-picked rows, enough to
// exercise the JSON-RPC handling and each tool. See test/setup.ts.
export default defineWorkersConfig({
  test: {
    setupFiles: ["./test/setup.ts"],
    poolOptions: {
      workers: {
        wrangler: { configPath: "./wrangler.jsonc" },
        miniflare: {
          d1Databases: { DB: "test-db" },
        },
      },
    },
  },
});
