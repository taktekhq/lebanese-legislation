// Minimal ambient types for the Cloudflare Workers runtime APIs this
// Worker uses (D1, Rate Limiting, ExecutionContext). Written by hand
// instead of depending on @cloudflare/workers-types, which pulled in a
// wrangler version newer than this repo's and broke npm's peer resolution;
// these cover exactly what src/*.ts calls, nothing more.

interface D1Result<T = unknown> {
  results: T[];
  success: boolean;
  meta: Record<string, unknown>;
}

interface D1PreparedStatement {
  bind(...values: unknown[]): D1PreparedStatement;
  all<T = unknown>(): Promise<D1Result<T>>;
  first<T = unknown>(): Promise<T | null>;
  run(): Promise<D1Result>;
}

interface D1Database {
  prepare(query: string): D1PreparedStatement;
  batch<T = unknown>(statements: D1PreparedStatement[]): Promise<D1Result<T>[]>;
  exec(query: string): Promise<{ count: number; duration: number }>;
}

interface RateLimitOutcome {
  success: boolean;
}

interface RateLimit {
  limit(options: { key: string }): Promise<RateLimitOutcome>;
}

interface ScheduledController {
  cron: string;
  scheduledTime: number;
}

interface ExecutionContext {
  waitUntil(promise: Promise<unknown>): void;
  passThroughOnException(): void;
}

interface Env {
  DB: D1Database;
  MCP_RATE_LIMITER: RateLimit;
  // About one PostHog event per IP per minute for the requests
  // MCP_RATE_LIMITER refuses. Without it, every refusal is logged.
  MCP_REFUSAL_LOG?: RateLimit;
  // GA4 Measurement Protocol (src/ga.ts), one stream per source; a stream
  // is off unless both its ID and its secret are set.
  GA_MCP_MEASUREMENT_ID?: string;
  GA_MCP_API_SECRET?: string;
  GA_HF_MEASUREMENT_ID?: string;
  GA_HF_API_SECRET?: string;
  // PostHog (src/posthog.ts): the project token (public, a var), the
  // capture host, and the secret that salts visitor ids. Off unless the
  // token is set.
  POSTHOG_KEY?: string;
  POSTHOG_HOST?: string;
  POSTHOG_ID_SALT?: string;
}

// lib.dom.d.ts's CacheStorage type has no "default" member (that's a
// Workers-runtime extension @cloudflare/workers-types would normally add);
// src/index.ts casts to this when reaching for caches.default instead of
// redeclaring the global, since lib.dom already declares `caches` itself.
interface WorkersCacheStorage {
  default: Cache;
}

// Lets test/*.ts's `import { env } from "cloudflare:test"` type-check as
// this Worker's own Env (see @cloudflare/vitest-pool-workers's docs on
// ProvidedEnv).
declare module "cloudflare:test" {
  interface ProvidedEnv extends Env {}
}
