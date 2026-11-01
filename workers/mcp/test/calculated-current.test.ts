import { env } from "cloudflare:test";
import { beforeAll, beforeEach, describe, expect, it } from "vitest";
import { getArticle, getLawTexts, searchLegislation } from "../src/tools";
import { mcpInitialize, toolDefs } from "../src/mcp";

const source = "تحدد القيمة بمبلغ 100000 ليرة لبنانية";
const calculated = "تحدد القيمة بمبلغ 5000000 ليرة لبنانية (خمسة ملايين ليرة لبنانية)";
let record: Record<string, any>;
const result = (r: any) => r.body.result;

beforeAll(async () => {
  await env.DB.exec("ALTER TABLE legislation_article ADD COLUMN calculated_current TEXT;");
  await env.DB.prepare("INSERT INTO legislation (id, title, title_norm, status, article_count) VALUES (244565, 'اصول المحاكمات المدنية', 'اصول المحاكمات المدنيه', 'in_force', 1)").run();
  await env.DB.prepare("INSERT INTO legislation_article (id, law_id, position, number, text_ar, text_norm, current_text_ar, version) VALUES (86, 244565, 1, '86', 'نص المركز السابق', '', ?, '2.0')").bind(source).run();
  // Snapshot builder indexes both published and calculated text.
  await env.DB.prepare("INSERT INTO article_search (law_id, number, text_norm) VALUES (244565, '86', ?)").bind(source + " " + calculated).run();
});
beforeEach(async () => {
  const hash = Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(source))), b => b.toString(16).padStart(2, "0")).join("");
  record = { kind: "statutory_monetary_adjustment", text_ar: calculated, source_text_ar: source, source_sha256: hash, is_published_wording: false, multiplier: 50,
    authority: { law_id: 330819, article: "1", url: "https://lebaneselegislation.com/decree/330819/article/1/", effective_on: "2024-09-19" } };
  await env.DB.prepare("UPDATE legislation SET status = 'in_force' WHERE id = 244565").run();
  await env.DB.prepare("UPDATE legislation_article SET law_id = 244565, current_text_ar = ?, calculated_current = ?, state = 'in_force', pending = 0, valid_from = '2024-09-19', in_force_from = NULL WHERE id = 86").bind(source, JSON.stringify(record)).run();
});
async function updateRecord() {
  await env.DB.prepare("UPDATE legislation_article SET calculated_current = ? WHERE id = 86").bind(JSON.stringify(record)).run();
}
async function article() { return result(await getArticle(env.DB, { law_id: 244565, number: "86" })); }
async function search() { return result(await searchLegislation(env.DB, { query: "5000000", kind: "articles" })); }

describe("exported calculated_current", () => {
  it("adds the exported object without changing published wording or provenance", async () => {
    const a = await article();
    expect(a.calculated_current).toEqual(record);
    expect(a.text_ar).toBe(source);
    expect(a.centre_text_ar).toBe("نص المركز السابق");
    expect(a.version).toBe("2.0");
    expect(a.url).toContain("/244565/article/86/");
  });
  it("exposes the same object in bulk texts and indexed updated-amount search hits", async () => {
    expect(result(await getLawTexts(env.DB, { law_id: 244565 })).articles[0].calculated_current).toEqual(record);
    const hits = (await search()).articles;
    expect(hits).toHaveLength(1);
    expect(hits[0].calculated_current).toEqual(record);
    expect(hits[0].snippet).toBe("نص المركز السابق");
  });
  it.each([
    ["stale source", "UPDATE legislation_article SET current_text_ar = 'تغير النص' WHERE id = 86"],
    ["repealed article", "UPDATE legislation_article SET state = 'repealed' WHERE id = 86"],
    ["annulled article", "UPDATE legislation_article SET state = 'annulled' WHERE id = 86"],
    ["pending wording", "UPDATE legislation_article SET pending = 1 WHERE id = 86"],
    ["future current wording", "UPDATE legislation_article SET valid_from = '2999-01-01' WHERE id = 86"],
    ["future original wording", "UPDATE legislation_article SET in_force_from = '2999-01-01' WHERE id = 86"],
    ["repealed law", "UPDATE legislation SET status = 'repealed' WHERE id = 244565"],
    ["annulled law", "UPDATE legislation SET status = 'annulled' WHERE id = 244565"],
  ])("suppresses %s on all surfaces", async (_, sql) => {
    await env.DB.exec(sql);
    expect((await article()).calculated_current).toBeUndefined();
    expect(result(await getLawTexts(env.DB, { law_id: 244565 })).articles[0].calculated_current).toBeUndefined();
    expect((await search()).articles[0].calculated_current).toBeUndefined();
  });
  it.each([
    ["wrong hash", { source_sha256: "0".repeat(64) }],
    ["wrong source", { source_text_ar: "stale" }],
    ["wrong multiplier", { multiplier: 5 }],
    ["published claim", { is_published_wording: true }],
    ["wrong kind", { kind: "other" }],
    ["empty calculation", { text_ar: "" }],
    ["wrong authority", { authority: { law_id: 330819 } }],
  ])("rejects %s", async (_, patch) => {
    Object.assign(record, patch);
    await updateRecord();
    expect((await article()).calculated_current).toBeUndefined();
  });
  it("rejects other laws even with a valid exported object", async () => {
    await env.DB.prepare("UPDATE legislation_article SET calculated_current = ?, text_ar = ? WHERE id = 1").bind(JSON.stringify(record), source).run();
    expect(result(await getArticle(env.DB, { law_id: 100, number: "5" })).calculated_current).toBeUndefined();
  });
  it.each(["{", "null", "[]", "42"])("ignores malformed JSON object %s", async raw => {
    await env.DB.prepare("UPDATE legislation_article SET calculated_current = ? WHERE id = 86").bind(raw).run();
    expect((await article()).calculated_current).toBeUndefined();
  });
  it("supports original source when current text is absent", async () => {
    await env.DB.prepare("UPDATE legislation_article SET current_text_ar = NULL, text_ar = ? WHERE id = 86").bind(source).run();
    expect((await article()).calculated_current).toEqual(record);
  });
  it("keeps old snapshots without the optional column usable", async () => {
    await env.DB.exec("ALTER TABLE legislation_article DROP COLUMN calculated_current;");
    expect((await article()).text_ar).toBe(source);
    expect((await article()).calculated_current).toBeUndefined();
    expect(result(await getLawTexts(env.DB, { law_id: 244565 })).articles[0].text_ar).toBe(source);
    expect((await search()).articles[0].calculated_current).toBeUndefined();
  });
});

it("instructs machine clients to distinguish calculated current amounts from quotations", () => {
  for (const name of ["get_article", "get_law_texts", "search_legislation"]) {
    const description = toolDefs.find(t => t.name === name)!.description;
    expect(description).toContain("calculated_current");
    expect(description).toContain("authority");
    expect(description).toContain("verbatim");
  }
  expect(mcpInitialize({}).instructions).toContain("calculated_current");
});
