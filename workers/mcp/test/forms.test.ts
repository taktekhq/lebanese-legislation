// Word forms, named codes, the Gazette number and the fallbacks of
// search_legislation (23 Oct 2026), against the fixture in test/setup.ts.

import { describe, expect, it } from "vitest";
import worker from "../src/index";
import { fts5Forms, lightStem } from "../src/arnorm";
import { gazetteNumber } from "../src/tools";
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
  expect(body.error, JSON.stringify(body.error)).toBeUndefined();
  return JSON.parse(body.result.content[0].text).result;
}

describe("word forms", () => {
  it("strips an article and an ending, keeping three letters", () => {
    expect(lightStem("الصوريه")).toBe("صوري");
    expect(lightStem("المحاكمات")).toBe("محاكم");
    expect(lightStem("والعقود")).toBe("عقود");
    expect(lightStem("سنه")).toBe("سنه"); // two letters would be left
  });

  it("matches a word bare or after an article, as a prefix, and keeps numbers exact", () => {
    const m = fts5Forms("الصورية في 257");
    expect(m).toContain('"صوري"*');
    expect(m).toContain('"والصوري"*');
    expect(m).not.toContain('"في"');
    expect(m.endsWith('"257"')).toBe(true);
  });

  it("finds an article by another form of its word, and says so", async () => {
    const r = await search({ query: "والخامسة", kind: "articles" });
    expect(r.searched_instead).toContain("any of its forms");
    expect(r.articles.some((a: { law: { id: number }; article_number: string }) => a.law.id === 100 && a.article_number === "5")).toBe(true);
  });

  it("finds a ruling by another form of its word", async () => {
    const r = await search({ query: "والتمييزي", kind: "rulings" });
    expect(r.rulings.some((x: { id: number }) => x.id === 9000)).toBe(true);
  });
});

describe("named codes", () => {
  it("opens the articles a code's name and numbers point to", async () => {
    const r = await search({ query: "قانون معدل 7-1 6", kind: "articles" });
    expect(r.articles.map((a: { law: { id: number }; article_number: string }) => `${a.law.id}/${a.article_number}`).slice(0, 2)).toEqual(["400/7-1", "400/6"]);
  });

  it("takes a code's name without «الجديد»", async () => {
    // the fixture's code 400 is «قانون معدل»; the rule is the same for «قانون السير الجديد»
    const r = await search({ query: "المادة 6 قانون معدل الجديد", kind: "articles" });
    expect(r.articles[0].article_number).toBe("6");
  });

  it("finds a code whose name the question gives without «قانون»", async () => {
    // «اصول المحاكمات الجزائية» (420) has no articles in the fixture; «قانون معدل» does
    const r = await search({ query: "المادة 6 معدل", kind: "articles" });
    expect(r.articles[0].law.id).toBe(400);
  });

  it("says when the code named holds none of the other words", async () => {
    const r = await search({ query: "قانون معدل الخامسة", kind: "articles" });
    expect(r.articles_not_in_code).toBe("قانون معدل");
    expect(r.articles[0].law.id).toBe(100);
  });

  it("reads المادة before the number", async () => {
    const r = await search({ query: "المادة 6 من القانون المعدل", kind: "articles" });
    expect(r.articles[0].law.id).toBe(400);
    expect(r.articles[0].article_number).toBe("6");
  });
});

describe("gazette issue numbers", () => {
  it("takes a bare number or a number after a Gazette word", () => {
    expect(gazetteNumber("123")).toBe("123");
    expect(gazetteNumber("الجريدة الرسمية عدد 39")).toBe("39");
    expect(gazetteNumber("قانون 251 15/4/2014")).toBe("");
  });

  it("does not fail on a long Arabic search", async () => {
    // over D1's 50-byte LIKE limit; this was "internal error" (23 Oct 2026)
    const r = await search({ query: "تبليغ المدعى عليه بالذات الجلسة الأولى التبليغ في محل الإقامة انعقاد المحاكمة" });
    expect(r.gazette).toEqual([]);
  });
});

describe("years and fallbacks", () => {
  it("does not look for the filter year in titles", async () => {
    const r = await search({ query: "الجرائم المعلوماتية 2018", kind: "laws", year: 2018 });
    expect(r.laws.some((l: { id: number }) => l.id === 500)).toBe(true);
  });

  it("rescues a question with a year filter", async () => {
    const r = await search({ query: "اتفاق الجرائم المعلوماتية", kind: "laws", year: 2018 });
    expect(r.laws.some((l: { id: number }) => l.id === 500)).toBe(true);
    expect(r.searched_instead).toBeTruthy();
  });

  it("sets aside the kind word of a two-word question, not the other", async () => {
    const r = await search({ query: "قانون المعلوماتيةغائب الجرائم", kind: "laws", year: 2018 });
    expect(r.laws.some((l: { id: number }) => l.id === 500)).toBe(true);
  });

  it("sets aside the one word that keeps every text out", async () => {
    const r = await search({ query: "نص الخامسة قانون غائبكلمة", kind: "articles" });
    expect(r.articles.length).toBeGreaterThan(0);
    expect(r.searched_instead).not.toContain("غائبكلمة");
  });
});
