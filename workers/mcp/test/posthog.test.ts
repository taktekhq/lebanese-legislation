// PostHog sender tests: nothing is sent without a key, one /batch/ POST per
// request with the visitor's IP and a stable id, long strings clipped, and a
// PostHog failure never reaches the caller. The outbound fetch is stubbed;
// no network.

import { afterEach, describe, expect, it, vi } from "vitest";
import { clipProps, phCapture, visitorId } from "../src/posthog";
import { permissiveEnv } from "./helpers";

const UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36";

function phEnv(extra: Partial<Env> = {}): Env {
  return { ...permissiveEnv(), POSTHOG_KEY: "phc_test", POSTHOG_HOST: "https://ph.example", ...extra };
}

function fakeCtx(): ExecutionContext & { settle: () => Promise<unknown> } {
  const pending: Promise<unknown>[] = [];
  return {
    waitUntil: (p) => { pending.push(p); },
    passThroughOnException: () => {},
    settle: () => Promise.all(pending),
  };
}

function request(): Request {
  return new Request("https://lebaneselegislation.com/mcp", { headers: { "CF-Connecting-IP": "203.0.113.9", "User-Agent": UA } });
}

afterEach(() => vi.restoreAllMocks());

describe("phCapture", () => {
  it("sends nothing without POSTHOG_KEY", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(null));
    const ctx = fakeCtx();
    phCapture({ ...permissiveEnv() }, ctx, request(), [{ event: "e", properties: {} }]);
    await ctx.settle();
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("posts one batch to /batch/ with the visitor's IP and id", async () => {
    const sent: Array<{ url: string; body: any }> = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      sent.push({ url: String(input), body: JSON.parse(String(init?.body)) });
      return new Response(null);
    });
    const env = phEnv();
    const ctx = fakeCtx();
    phCapture(env, ctx, request(), [{ event: "a", properties: { x: 1 } }, { event: "b", properties: {} }]);
    await ctx.settle();
    expect(sent).toHaveLength(1);
    expect(sent[0].url).toBe("https://ph.example/batch/");
    expect(sent[0].body.api_key).toBe("phc_test");
    const id = await visitorId(env, "203.0.113.9", UA);
    expect(sent[0].body.batch.map((e: any) => e.event)).toEqual(["a", "b"]);
    for (const e of sent[0].body.batch) {
      expect(e.properties.distinct_id).toBe(id);
      expect(e.properties.$ip).toBe("203.0.113.9");
      expect(e.properties.$lib).toBe("lebaneselegislation-worker");
    }
    expect(sent[0].body.batch[0].properties.x).toBe(1);
  });

  it("never throws when PostHog rejects", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new Error("down"));
    vi.spyOn(console, "error").mockImplementation(() => {});
    const ctx = fakeCtx();
    phCapture(phEnv(), ctx, request(), [{ event: "e", properties: {} }]);
    await expect(ctx.settle()).resolves.toBeDefined();
  });
});

describe("visitorId", () => {
  it("is stable across calls and differs by salt", async () => {
    const a1 = await visitorId(phEnv({ POSTHOG_ID_SALT: "a" }), "203.0.113.9", UA);
    const a2 = await visitorId(phEnv({ POSTHOG_ID_SALT: "a" }), "203.0.113.9", UA);
    const b = await visitorId(phEnv({ POSTHOG_ID_SALT: "b" }), "203.0.113.9", UA);
    expect(a1).toBe(a2);
    expect(a1).not.toBe(b);
    expect(a1).toMatch(/^[0-9a-f]{64}$/);
  });
});

describe("clipProps", () => {
  it("clips long strings, recursing into objects and arrays", () => {
    const long = "x".repeat(20000);
    const out = clipProps({ s: long, n: 3, o: { a: { b: long } }, l: [long] }) as any;
    expect(out.s).toHaveLength(10000);
    expect(out.n).toBe(3);
    expect(out.o.a.b).toHaveLength(10000);
    expect(out.l[0]).toHaveLength(10000);
  });
});
