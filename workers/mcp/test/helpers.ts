// The miniflare version @cloudflare/vitest-pool-workers pins (4.20250906.0)
// predates Workers Rate Limiting binding support, so env.MCP_RATE_LIMITER
// is undefined under cloudflare:test. Tests build their own Env from the
// real D1 binding plus a small in-process stand-in for the limiter, so
// everything but the limiter itself still runs against the real D1 code
// path; test/mcp.test.ts's own rate-limit test uses a second, stricter
// stand-in to check the 429 behavior specifically.

import { env as cfEnv } from "cloudflare:test";

export function permissiveEnv(): Env {
  return {
    DB: cfEnv.DB,
    MCP_RATE_LIMITER: { limit: async () => ({ success: true }) },
  };
}

// A real (if simplified) token bucket: `limit` tokens per 60s window per
// key, refilling continuously, same shape as the Workers binding.
export function fakeRateLimiter(limit: number): RateLimit {
  const buckets = new Map<string, { tokens: number; last: number }>();
  const perMs = limit / 60000;
  return {
    async limit({ key }) {
      const now = Date.now();
      let b = buckets.get(key);
      if (!b) {
        b = { tokens: limit, last: now };
        buckets.set(key, b);
      } else {
        b.tokens = Math.min(limit, b.tokens + (now - b.last) * perMs);
        b.last = now;
      }
      if (b.tokens < 1) return { success: false };
      b.tokens--;
      return { success: true };
    },
  };
}
