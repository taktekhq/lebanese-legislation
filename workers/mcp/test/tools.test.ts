// One test per tool, through the real JSON-RPC path (tools/call), against
// the fixture seeded by test/setup.ts: law 100 (article 5), amending
// decree 200 (article 1, which amends law 100's article 5), a general
// repeal law 300 that possibly affects law 100, ruling 9000, gazette 500.

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
  return { isError: body.result.isError as boolean, result: parsed.result, notAdvice: parsed.not_legal_advice as string };
}

describe("search_legislation", () => {
  it("finds the seeded law by a word from its title", async () => {
    const { result } = await call("search_legislation", { query: "الاختبار", kind: "laws" });
    expect(result.laws.some((l: { id: number }) => l.id === 100)).toBe(true);
  });

  it("finds the seeded article by a word from its text", async () => {
    const { result } = await call("search_legislation", { query: "الخامسة", kind: "articles" });
    expect(result.articles.some((a: { article_number: string; law: { id: number } }) => a.law.id === 100 && a.article_number === "5")).toBe(true);
  });

  it("finds the seeded ruling", async () => {
    const { result } = await call("search_legislation", { query: "تمييزي", kind: "rulings" });
    expect(result.rulings.some((r: { id: number }) => r.id === 9000)).toBe(true);
    const hit = result.rulings.find((r: { id: number }) => r.id === 9000);
    expect(hit.pdf_url).toBe("https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=9000&type=list");
  });

  it("finds the seeded gazette issue by its number", async () => {
    const { result } = await call("search_legislation", { query: "123", kind: "gazette" });
    expect(result.gazette.some((g: { id: number }) => g.id === 500)).toBe(true);
  });

  it("rejects an unknown kind", async () => {
    const { isError } = await call("search_legislation", { query: "x", kind: "bogus" });
    expect(isError).toBe(true);
  });

  it("every result carries the not-legal-advice line", async () => {
    const { notAdvice } = await call("search_legislation", { query: "الاختبار" });
    expect(notAdvice).toContain("Not legal advice");
  });
});

describe("get_law", () => {
  it("returns metadata and the table of contents", async () => {
    const { result } = await call("get_law", { id: 100 });
    expect(result.id).toBe(100);
    expect(result.kind_en).toBe("Law");
    expect(result.articles).toEqual([{ position: 1, number: "5", url: expect.stringContaining("/law/100/article/5/") }]);
    expect(result.table_of_contents).toEqual([]);
    expect(result.articles_truncated).toBeUndefined();
  });

  it("returns the chapters and the chapter each article sits under", async () => {
    const { result } = await call("get_law", { id: 400 });
    expect(result.table_of_contents).toEqual([
      { id: 41, depth: 0, heading: "الباب الاول: احكام عامة" },
      { id: 42, parent_id: 41, depth: 1, heading: "الفصل الاول" },
    ]);
    expect(result.articles.map((a: { number: string; section_id: number }) => [a.number, a.section_id]))
      .toEqual([["6", 41], ["7", 42], ["7-1", 42]]);
  });

  it("404s (as a tool error) for an id that does not exist", async () => {
    const { isError } = await call("get_law", { id: 999999 });
    expect(isError).toBe(true);
  });
});

describe("get_article", () => {
  it("returns verbatim text and the amending article that changed it, oldest first", async () => {
    const { result } = await call("get_article", { law_id: 100, number: "5" });
    expect(result.text_ar).toBe("نص المادة الخامسة من قانون الاختبار");
    expect(result.text_fr).toContain("Texte de l'article");
    expect(result.changes).toHaveLength(1);
    expect(result.changes[0].relation).toBe("amend");
    expect(result.changes[0].by.id).toBe(200);
    expect(result.changes[0].by_article).toBe("1");
    expect(result.changes[0].text_ar).toBe("تعدل المادة الخامسة من قانون الاختبار كما يلي");
    expect(result.possibly_affected_by).toHaveLength(1);
    expect(result.possibly_affected_by[0].by.id).toBe(300);
  });

  it("returns the wording in force today, with the Centre's earlier wording kept apart", async () => {
    const { result } = await call("get_article", { law_id: 400, number: "6" });
    expect(result.text_ar).toBe("نص المادة السادسة الجديد");
    expect(result.text_fr).toBeUndefined();
    expect(result.version).toBe("2.0");
    expect(result.current_since).toBe("2023-12-28");
    expect(result.centre_text_ar).toBe("نص المادة السادسة القديم");
    expect(result.centre_text_fr).toContain("Ancien texte");
    expect(result.centre_error).toEqual({ en: "The Centre's text repeats article 5.", ar: "يكرر نص المادة 5." });
    expect(result.repealed).toBe(false);
  });

  it("gives no current text for a repealed article", async () => {
    const { result } = await call("get_article", { law_id: 400, number: "7" });
    expect(result.text_ar).toBeUndefined();
    expect(result.repealed).toBe(true);
    expect(result.centre_text_ar).toBe("نص المادة السابعة");
  });

  it("leaves an unamended article as it was", async () => {
    const { result } = await call("get_article", { law_id: 100, number: "5" });
    expect(result.centre_text_ar).toBeUndefined();
    expect(result.version).toBe("1.0");
  });

  it("is a tool error for an article number that doesn't exist in the law", async () => {
    const { isError } = await call("get_article", { law_id: 100, number: "999" });
    expect(isError).toBe(true);
  });
});

describe("articles numbered under another", () => {
  for (const asked of ["7-1", "7 (1)", "7/1", "7 - 1", "٧-١", "المادة 7-1"]) {
    it(`reads ${JSON.stringify(asked)} as article 7-1`, async () => {
      const { result, isError } = await call("get_article", { law_id: 400, number: asked });
      expect(isError).toBe(false);
      expect(result.number).toBe("7-1");
      expect(result.text_ar).toBe("يبرم العقد خطيا");
      expect(result.section_id).toBe(42);
    });
  }
});

describe("get_law_texts", () => {
  it("pages through a law's articles with the same text rules as get_article", async () => {
    const first = (await call("get_law_texts", { law_id: 400, limit: 2 })).result;
    expect(first.total).toBe(3);
    expect(first.next_offset).toBe(2);
    expect(first.articles.map((a: { number: string }) => a.number)).toEqual(["6", "7"]);
    expect(first.articles[0].text_ar).toBe("نص المادة السادسة الجديد");
    expect(first.articles[0].centre_text_ar).toBe("نص المادة السادسة القديم");
    expect(first.articles[0].section_id).toBe(41);
    expect(first.articles[0].centre_error.en).toContain("repeats article 5");
    expect(first.articles[1].centre_error).toBeUndefined();
    expect(first.articles[1].text_ar).toBeUndefined();
    expect(first.articles[1].repealed).toBe(true);
    const second = (await call("get_law_texts", { law_id: 400, offset: first.next_offset, limit: 2 })).result;
    expect(second.articles.map((a: { number: string }) => a.number)).toEqual(["7-1"]);
    expect(second.articles[0].text_fr).toBe("Le contrat est conclu par écrit.");
    expect(second.next_offset).toBeUndefined();
  });

  it("is a tool error for a law that does not exist", async () => {
    const { isError } = await call("get_law_texts", { law_id: 999999 });
    expect(isError).toBe(true);
  });
});

describe("search beyond Arabic words", () => {
  it("finds an article by a French word, plural or accented", async () => {
    for (const q of ["contrat", "contrats", "écrit", "ecrit"]) {
      const { result } = await call("search_legislation", { query: q, kind: "articles" });
      expect(result.articles.some((a: { article_number: string; law: { id: number } }) => a.law.id === 400 && a.article_number === "7-1"), q).toBe(true);
    }
  });

  it("finds a law by its id", async () => {
    const { result } = await call("search_legislation", { query: "100", kind: "laws" });
    expect(result.laws[0].id).toBe(100);
  });

  it("finds a code by its French or English name", async () => {
    for (const q of ["Code des essais", "essais", "test code"]) {
      const { result } = await call("search_legislation", { query: q, kind: "laws" });
      expect(result.laws.some((l: { id: number }) => l.id === 400), q).toBe(true);
    }
  });

  it("ranks the code whose name is closest first", async () => {
    for (const q of ["Penal Code", "penal code", "Code pénal", "code penal", "penal"]) {
      const { result } = await call("search_legislation", { query: q, kind: "laws" });
      expect(result.laws[0].id, q).toBe(430);
    }
    const { result } = await call("search_legislation", { query: "criminal procedure", kind: "laws" });
    expect(result.laws[0].id).toBe(420);
  });
});

describe("get_ruling", () => {
  it("links the judgment's scan on the Lebanese University's site", async () => {
    const { result } = await call("get_ruling", { id: 9000 });
    expect(result.pdf_url).toBe("https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=9000&type=list");
  });
  it("returns the ruling's verbatim text and panel", async () => {
    const { result } = await call("get_ruling", { id: 9000 });
    expect(result.title).toBe("حكم تمييزي رقم 15");
    expect(result.court_en).toBe("Court of Cassation");
    expect(result.members).toEqual(["قاض اول", "قاض ثان"]);
    expect(result.text_ar).toContain("قانون الاختبار");
  });
});

describe("get_gazette_issue", () => {
  it("returns the issue and the law it published", async () => {
    const { result } = await call("get_gazette_issue", { id: 500 });
    expect(result.number).toBe("123");
    expect(result.texts.map((t: { id: number }) => t.id)).toContain(100);
  });
});

describe("get_links", () => {
  it("returns the edge into an article (direction: to)", async () => {
    const { result } = await call("get_links", { key: "law/100/art/5", direction: "to" });
    // Raw edges: the model's and the parser's reading of the same change.
    expect(result.links).toHaveLength(2);
    expect(result.links[0].relation).toBe("amend");
    expect(result.links[0].from_key).toBe("law/200/art/1");
  });

  it("returns the edge out of the amending article (direction: from)", async () => {
    const { result } = await call("get_links", { key: "law/200/art/1", direction: "from" });
    expect(result.links).toHaveLength(2);
    expect(result.links[0].to_key).toBe("law/100/art/5");
  });

  it("rejects a malformed key", async () => {
    const { isError } = await call("get_links", { key: "not-a-key" });
    expect(isError).toBe(true);
  });
});

describe("possibly_affected", () => {
  it("lists the general repeal that may touch the law", async () => {
    const { result } = await call("possibly_affected", { law_id: 100 });
    expect(result.possibly_affected_by).toHaveLength(1);
    expect(result.possibly_affected_by[0].by.id).toBe(300);
  });
});
