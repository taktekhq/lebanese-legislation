import { env } from "cloudflare:test";
import { describe, expect, it } from "vitest";
import { getArticle, getGazetteIssue, getLaw, getRuling } from "../src/tools";

// updated_at is the day a page last changed on the site (page_state, carried
// by the snapshot's updated_at columns). A snapshot older than the columns,
// like the shared setup's, answers without it.
async function result(p: Promise<{ body: unknown }>) {
  return ((await p).body as { result: any }).result;
}

async function addColumns() {
  for (const t of ["legislation", "ruling", "gazette_issue"]) {
    await env.DB.prepare(`ALTER TABLE ${t} ADD COLUMN updated_at TEXT`).run();
  }
  await env.DB.prepare("UPDATE legislation SET updated_at = '2026-10-30' WHERE id = 100").run();
  await env.DB.prepare("UPDATE ruling SET updated_at = '2026-10-29' WHERE id = 9000").run();
}

describe("updated_at", () => {
  it("is absent on an older snapshot", async () => {
    expect((await result(getLaw(env.DB, { id: 100 }))).updated_at).toBeUndefined();
    expect((await result(getRuling(env.DB, { id: 9000 }))).updated_at).toBeUndefined();
  });
  it("comes from the snapshot for texts, articles, rulings and the texts of an issue", async () => {
    await addColumns();
    expect((await result(getLaw(env.DB, { id: 100 }))).updated_at).toBe("2026-10-30");
    expect((await result(getLaw(env.DB, { id: 200 }))).updated_at).toBeUndefined();
    const a = await result(getArticle(env.DB, { law_id: 100, number: "5" }));
    expect(a.updated_at).toBe("2026-10-30");
    expect(a.law.updated_at).toBe("2026-10-30");
    expect((await result(getRuling(env.DB, { id: 9000 }))).updated_at).toBe("2026-10-29");
    const g = await result(getGazetteIssue(env.DB, { id: 500 }));
    expect(g.updated_at).toBeUndefined();
    expect(g.texts[0].updated_at).toBe("2026-10-30");
  });
});
