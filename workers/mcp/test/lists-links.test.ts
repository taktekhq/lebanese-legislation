// The listing tools, get_links paging and scope, article-level amended_by /
// amended in get_law, and the Gazette part of search_legislation. Fixture
// (test/setup.ts): Law 3/2025 (id 333455, Gazette 22 of 2025-05-16) inserting
// ten articles into the Labour Code (190374); Law 68/2026 (id 2000200346, a
// Gazette scan, issue 2002026036) amending Penal Code (430) article 37; decree
// 200 changing the Penal Code as a whole and its article 99 (a 2,000 character
// quote); a text with no date at all (600600).

import { describe, expect, it } from "vitest";
import worker from "../src/index";
import { permissiveEnv } from "./helpers";

async function call(name: string, args: Record<string, unknown> = {}) {
  const res = await worker.fetch(
    new Request("https://lebaneselegislation.com/mcp", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name, arguments: args } }),
    }),
    permissiveEnv(),
  );
  const body = await res.json();
  expect(body.error, `tools/call ${name} returned a JSON-RPC error: ${JSON.stringify(body.error)}`).toBeUndefined();
  const parsed = JSON.parse(body.result.content[0].text);
  return { isError: body.result.isError as boolean, result: parsed.result ?? parsed, };
}

const ids = (xs: { id: number }[]) => xs.map((x) => x.id);

describe("get_law amended_by / amended with article-level changes", () => {
  it("lists Law 3/2025 once under the Labour Code, with the articles it inserted", async () => {
    const { result } = await call("get_law", { id: 190374 });
    expect(result.amended_by).toHaveLength(1);
    const e = result.amended_by[0];
    expect(e.relation).toBe("insert");
    expect(e.verb).toBe("Added to");
    expect(e.law.id).toBe(333455);
    expect(e.effective_on).toBe("2025-05-16");
    expect(e.articles).toEqual(["3", "4", "5", "6", "7", "8", "9", "10", "11", "12"]);
  });

  it("shows the same change under amended on the amending law", async () => {
    const { result } = await call("get_law", { id: 333455 });
    expect(result.amended).toHaveLength(1);
    expect(result.amended[0].law.id).toBe(190374);
    expect(result.amended[0].articles).toHaveLength(10);
    expect(result.amended_by).toEqual([]);
  });

  it("lists a Gazette-scan law that amends Penal article 37, and a whole-law change without articles", async () => {
    const { result } = await call("get_law", { id: 430 });
    // the edge from a ruling that shares the scan's id is not a law
    const by = result.amended_by.map((e: { relation: string; law: { id: number }; articles?: string[] }) => [e.relation, e.law.id, e.articles]);
    expect(by).toEqual([["replace", 200, undefined], ["amend", 300, ["99"]], ["amend", 2000200346, ["37"]]]);
    const scan = (await call("get_law", { id: 2000200346 })).result;
    expect(scan.amended).toHaveLength(1);
    expect(scan.amended[0]).toMatchObject({ relation: "amend", articles: ["37"], effective_on: "2026-09-10" });
    expect(scan.amended[0].law.id).toBe(430);
  });

  it("keeps the old shape for a change to an article of an old law", async () => {
    const { result } = await call("get_law", { id: 100 });
    expect(result.amended_by).toHaveLength(1);
    expect(result.amended_by[0]).toMatchObject({ relation: "amend", articles: ["5"] });
    expect(result.amended_by[0].law.id).toBe(200);
  });
});

describe("get_links on a law", () => {
  it("includes the edges of its articles by default, newest source first, with total", async () => {
    const { result } = await call("get_links", { key: "law/430", direction: "to" });
    expect(result.scope).toBe("all");
    expect(result.order).toBe("newest");
    expect(result.total).toBe(4);
    expect(result.next_offset).toBeUndefined();
    expect(result.links.map((l: { to_key: string }) => l.to_key)).toEqual(["law/430/art/37", "law/430/art/99", "law/430", "law/430/art/5"]);
    expect(result.links[0].date).toBe("2026-09-10");
  });

  it("scope law is the law itself only", async () => {
    const { result } = await call("get_links", { key: "law/430", direction: "to", scope: "law" });
    expect(result.total).toBe(1);
    expect(result.links[0].to_key).toBe("law/430");
  });

  it("orders oldest first and pages with offset and next_offset", async () => {
    const first = (await call("get_links", { key: "law/430", order: "oldest", limit: 2 })).result;
    expect(first.links.map((l: { to_key: string }) => l.to_key)).toEqual(["law/430", "law/430/art/99"]);
    expect(first.next_offset).toBe(2);
    const second = (await call("get_links", { key: "law/430", order: "oldest", limit: 2, offset: first.next_offset })).result;
    expect(second.links.map((l: { to_key: string }) => l.to_key)).toEqual(["law/430/art/37", "law/430/art/5"]);
    expect(second.next_offset).toBeUndefined();
  });

  it("direction from takes in the edges out of its articles", async () => {
    const { result } = await call("get_links", { key: "law/333455", direction: "from", relation: "insert" });
    expect(result.total).toBe(11);
    expect(result.links[0].to_key).toMatch(/^law\/190374\/art\//);
  });

  it("cuts a very long quote and says so; limit 200 is safe", async () => {
    const { result, isError } = await call("get_links", { key: "law/430", limit: 200 });
    expect(isError).toBe(false);
    const long = result.links.find((l: { to_key: string }) => l.to_key === "law/430/art/99");
    expect([...long.quote]).toHaveLength(601);
    expect(long.quote_truncated).toBe(true);
    const short = result.links.find((l: { to_key: string }) => l.to_key === "law/430/art/37");
    expect(short.quote_truncated).toBeUndefined();
  });

  it("an article key stays exact", async () => {
    const { result } = await call("get_links", { key: "law/430/art/37", scope: "all" });
    expect(result.total).toBe(1);
    expect(result.scope).toBeUndefined();
  });

  it("rejects a bad order or scope", async () => {
    expect((await call("get_links", { key: "law/430", order: "sideways" })).isError).toBe(true);
    expect((await call("get_links", { key: "law/430", scope: "everything" })).isError).toBe(true);
  });
});

describe("list_legislation", () => {
  it("lists everything since 2025 newest first, scans included, by Gazette date else issue date", async () => {
    const { result } = await call("list_legislation", { since: "2025" });
    expect(result.since).toBe("2025");
    expect(result.order).toBe("newest");
    expect(result.total).toBe(3);
    expect(ids(result.laws)).toEqual([2000200347, 2000200346, 333455]);
    expect(result.laws[1]).toMatchObject({ date: "2026-09-10", gazette_number: "36", gazette_date: "2026-09-10", gazette_issue_id: 2002026036 });
    expect(result.laws[2].date).toBe("2025-05-16");
  });

  it("filters by month, kind (Arabic or English) and orders oldest first", async () => {
    expect(ids((await call("list_legislation", { since: "2025-05", until: "2025-05" })).result.laws)).toEqual([333455]);
    expect(ids((await call("list_legislation", { since: "2025", kind: "decree" })).result.laws)).toEqual([2000200347]);
    expect((await call("list_legislation", { since: "2025", kind: "مرسوم" })).result.total).toBe(1);
    expect((await call("list_legislation", { since: "2025", order: "oldest" })).result.laws[0].id).toBe(333455);
  });

  it("pages with offset and next_offset, and leaves out texts with no date", async () => {
    const p1 = (await call("list_legislation", { since: "2025", limit: 2 })).result;
    expect(p1.next_offset).toBe(2);
    const p2 = (await call("list_legislation", { since: "2025", limit: 2, offset: 2 })).result;
    expect(ids(p2.laws)).toEqual([333455]);
    expect(p2.next_offset).toBeUndefined();
    const all = (await call("list_legislation", { limit: 100 })).result;
    expect(ids(all.laws)).not.toContain(600600);
  });

  it("gives a clean error for bad dates and order", async () => {
    for (const args of [{ since: "2025-13" }, { since: "last year" }, { until: "2025-02-30" }, { since: "2026", until: "2025" }, { order: "sideways" }]) {
      const { isError, result } = await call("list_legislation", args);
      expect(isError, JSON.stringify(args)).toBe(true);
      expect(typeof result.error).toBe("string");
    }
  });
});

describe("list_gazette_issues", () => {
  it("lists issues newest first with text counts and their source", async () => {
    const { result } = await call("list_gazette_issues", { since: "2025" });
    expect(result.total).toBe(3);
    expect(ids(result.issues)).toEqual([2002026036, 601, 600]);
    expect(result.issues[0]).toEqual({ id: 2002026036, number: "36", published_on: "2026-09-10", text_count: 2, source: "scans", url: "https://lebaneselegislation.com/gazette/2002026036/" });
    expect(result.issues[1].text_count).toBe(0);
    expect(result.issues[2]).toMatchObject({ source: "centre", text_count: 1 });
  });

  it("filters by range, orders oldest first and pages", async () => {
    expect(ids((await call("list_gazette_issues", { since: "2025-05-20", until: "2025-05" })).result.issues)).toEqual([601]);
    const p = (await call("list_gazette_issues", { order: "oldest", limit: 2 })).result;
    expect(ids(p.issues)).toEqual([500, 600]);
    expect(p.next_offset).toBe(2);
    const q = (await call("list_gazette_issues", { order: "oldest", limit: 2, offset: 2 })).result;
    expect(ids(q.issues)).toEqual([601, 2002026036]);
    expect(q.next_offset).toBeUndefined();
  });

  it("gives a clean error for bad dates", async () => {
    expect((await call("list_gazette_issues", { since: "2025/01" })).isError).toBe(true);
    expect((await call("list_gazette_issues", { until: "x" })).isError).toBe(true);
  });
});

describe("search_legislation in the gazette", () => {
  it("a bare year lists that year's issues", async () => {
    const { result } = await call("search_legislation", { query: "2025", kind: "gazette" });
    expect(ids(result.gazette)).toEqual([601, 600]);
    expect(result.gazette_count).toBe(2);
  });

  it("22/2025 is issue 22 of 2025", async () => {
    expect(ids((await call("search_legislation", { query: "22/2025", kind: "gazette" })).result.gazette)).toEqual([600]);
    expect((await call("search_legislation", { query: "22/2026", kind: "gazette" })).result.gazette_count).toBe(0);
  });

  it("honours year on a number search, and keeps plain number search", async () => {
    expect(ids((await call("search_legislation", { query: "36", kind: "gazette", year: 2026 })).result.gazette)).toEqual([2002026036]);
    expect((await call("search_legislation", { query: "36", kind: "gazette", year: 2025 })).result.gazette_count).toBe(0);
    expect((await call("search_legislation", { query: "123", kind: "gazette" })).result.gazette[0].id).toBe(500);
  });
});
