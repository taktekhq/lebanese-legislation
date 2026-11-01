// The zero-result rescue (src/gloss.ts, rescueReadings in src/tools.ts) and the
// English titles, through the real tools/call path. The queries are real ones
// that found nothing before 21 Oct 2026 (perf-search.done.md).

import { describe, expect, it } from "vitest";
import worker from "../src/index";
import { foldDigits, glossQuery, nameVariants, pinnedLaw, splitNumbers } from "../src/gloss";
import { rescueReadings } from "../src/tools";
import { permissiveEnv } from "./helpers";

async function search(args: Record<string, unknown>) {
  const res = await worker.fetch(
    new Request("https://lebaneselegislation.com/mcp", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name: "search_legislation", arguments: args } }),
    }),
    permissiveEnv(),
  );
  const body = await res.json();
  return JSON.parse(body.result.content[0].text).result;
}

const ids = (r: { laws: { id: number }[] }) => r.laws.map((l) => l.id);

describe("gloss.ts", () => {
  it("folds Arabic-Indic digits", () => {
    expect(foldDigits("٩٥٩ اصول ۱۲")).toBe("959 اصول 12");
  });
  it("translates the glossary terms the Go side translates", () => {
    expect(glossQuery("Cybercrime")).toBe("الجرائم المعلوماتية");
    expect(glossQuery("civil marriage")).toBe("الزواج المدني");
    expect(glossQuery("banking secrecy")).toBe("السرية المصرفية");
    expect(glossQuery("Penalty")).toBe("عقوبة");
    expect(glossQuery("Lebanon’s labor law")).toBe("قانون العمل");
    expect(glossQuery("nationalité")).toBe("جنسية");
    expect(glossQuery("زواج")).toBe("");
    expect(glossQuery("what do you access to?")).toBe("");
  });
  it("pulls a number and a year out of a question", () => {
    expect(splitNumbers("513 هيئة إدارة السير")).toEqual({ num: "513", year: 0, words: ["هيئة", "إدارة", "السير"] });
    expect(splitNumbers("تعيين في وزارة الخارجية 2024")).toEqual({ num: "", year: 2024, words: ["تعيين", "وزارة", "الخارجية"] });
    expect(splitNumbers("٩٥٩ اصول محاكمات مدنيه")).toEqual({ num: "959", year: 0, words: ["اصول", "محاكمات", "مدنيه"] });
  });
  it("spells a Latin name other ways", () => {
    expect(nameVariants("Chehab")).toEqual(expect.arrayContaining(["chehab", "shehab", "shihab"]));
    expect(nameVariants("Chehab")[0]).toBe("chehab");
    expect(nameVariants("ab")).toEqual([]);
  });
  it("offers readings cheapest first", () => {
    expect(rescueReadings("civil marriage")[0]).toEqual({ query: "الزواج المدني", num: "", year: 0, kind: "translated" });
    expect(rescueReadings("513 هيئة إدارة السير")[0]).toEqual({ query: "هيئة إدارة السير", num: "513", year: 0, kind: "adjusted" });
  });
});

describe("search rescue", () => {
  it("finds nothing for a nonsense word and says nothing was searched instead", async () => {
    const r = await search({ query: "qqqzzz" });
    expect(r.law_count).toBe(0);
    expect(r.searched_instead).toBeUndefined();
  });
  it.each([
    ["civil marriage", "الزواج المدني", 501],
    ["banking secrecy", "السرية المصرفية", 502],
  ])("%s is read into Arabic", async (q, via, id) => {
    const r = await search({ query: q });
    expect(r.searched_instead).toBe(via);
    expect(r.searched_instead_kind).toBe("translated");
    expect(ids(r)).toContain(id);
  });
  it("uses a number in the question to narrow the texts", async () => {
    const r = await search({ query: "513 هيئة إدارة السير" });
    expect(r.searched_instead).toBe("هيئة إدارة السير 513");
    expect(r.searched_instead_kind).toBe("adjusted");
    expect(ids(r)).toEqual([503]);
  });
  it("uses a year in the question to narrow the texts", async () => {
    const r = await search({ query: "تعيين في وزارة الخارجية 2024" });
    expect(r.searched_instead).toContain("2024");
    expect(ids(r)).toEqual([504]);
    expect(r.article_count).toBeUndefined(); // a year names a text, not an article
  });
  it("reads Arabic-Indic digits, even in the first search", async () => {
    const r = await search({ query: "١٢٣", kind: "gazette" });
    expect(r.gazette_count).toBe(1);
    expect(r.q).toBe("123");
    expect(r.searched_instead).toBeUndefined();
  });
  it("tries another spelling of a Latin name against the English titles", async () => {
    const r = await search({ query: "Chehab", kind: "laws" });
    expect(ids(r)).toEqual([506]);
    expect(r.searched_instead).toBe("shehab");
  });
  it("does not rescue a search that found something", async () => {
    const r = await search({ query: "قانون الاختبار" });
    expect(r.searched_instead).toBeUndefined();
    expect(ids(r)).toContain(100);
  });
});

describe("pinned questions", () => {
  it.each([
    "cybercrime", "cyber crime", "cybercrimes", "Cyber-Crime", "information crimes", "computer crimes", "Lebanon computer crimes",
    "الجرائم المعلوماتية", "الجرائم الالكترونية", "الجرائم الإلكترونية", "جرائم المعلوماتية", "جرائم الكترونية",
  ])("%s finds Law 81 of 2018 first", async (q) => {
    const r = await search({ query: q, kind: "laws" });
    expect(ids(r)[0]).toBe(278573);
    expect(ids(r).filter((i: number) => i === 278573)).toHaveLength(1);
    const all = await search({ query: q });
    expect(ids(all)[0]).toBe(278573);
  });
  it("keeps the other matches after it", async () => {
    const r = await search({ query: "cybercrime", kind: "laws" });
    expect(ids(r)).toEqual([278573, 507]); // the grant decree for the cybercrime bureau follows
  });
  it("is not pinned for another year, or for other words", async () => {
    expect(ids(await search({ query: "cybercrime", kind: "laws", year: 1999 }))).not.toContain(278573);
    expect(ids(await search({ query: "قانون الاختبار", kind: "laws" }))).not.toContain(278573);
  });
  it("pinnedLaw reads a question like the Go side does", () => {
    expect(pinnedLaw("cyber crimes law")).toBe(278573);
    expect(pinnedLaw("قانون الجرائم المعلوماتية")).toBe(278573);
    expect(pinnedLaw("crime")).toBe(0);
    expect(pinnedLaw("")).toBe(0);
  });
});

describe("English titles", () => {
  it("carries title_en on laws that have one, and omits it otherwise", async () => {
    const r = await search({ query: "قانون الاختبار", kind: "laws" });
    const l100 = r.laws.find((l: { id: number }) => l.id === 100);
    expect(l100.title_en).toBe("The test law");
    expect(l100.title).toBe("قانون الاختبار"); // the Arabic title stays beside it
    const other = (await search({ query: "قانون الزواج المدني", kind: "laws" })).laws[0];
    expect("title_en" in other).toBe(false);
  });
  it("finds a law by its English title", async () => {
    const r = await search({ query: "information crimes", kind: "laws" });
    expect(ids(r)).toEqual([278573, 500]); // Law 81 of 2018 is pinned first, the English title follows
    expect(r.searched_instead).toBeUndefined();
    expect(r.laws[1].title_en).toBe("Law on information crimes");
  });
  it("ranks an English title match below Arabic matches", async () => {
    // "test" is a code's name (Arabic-side code match) and in law 100's English title
    const r = await search({ query: "test", kind: "laws" });
    expect(ids(r)[0]).toBe(400);
    expect(ids(r)).toContain(100);
  });
  it("get_law returns title_en", async () => {
    const res = await worker.fetch(
      new Request("https://lebaneselegislation.com/mcp", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name: "get_law", arguments: { id: 100 } } }),
      }),
      permissiveEnv(),
    );
    const out = JSON.parse((await res.json()).result.content[0].text).result;
    expect(out.title_en).toBe("The test law");
  });
});
