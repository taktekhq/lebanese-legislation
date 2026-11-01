import { env } from "cloudflare:test";
import { describe, expect, it } from "vitest";
import { getRuling } from "../src/tools";

const note = "Machine reading of the scanned judgment. It may contain errors; the scan (pdf_url) is the reference.";
const lowNote = " Pages with low_confidence: true are hard to read (mostly handwriting): expect errors.";
async function ruling() {
  const response = await getRuling(env.DB, { id: 9000 });
  return (response.body as { result: any }).result;
}
async function createPages() {
  await env.DB.prepare("CREATE TABLE ruling_page (ruling_id INTEGER, n INTEGER, text_ar TEXT, low_confidence INTEGER, PRIMARY KEY (ruling_id,n))").run();
}

describe("ruling machine reading", () => {
  it("returns ordered pages, flags only hard pages, and keeps the scan", async () => {
    await createPages();
    await env.DB.prepare("INSERT INTO ruling_page VALUES (9000,2,'صعب',1),(9000,1,?,0),(999,1,'other ruling',1)")
      .bind("نص \uE000واحد\uE001اثنان\uE002").run();
    const result = await ruling();
    expect(result.pages).toEqual([
      { n: 1, text_ar: "نص ⟦واحد | اثنان⟧" },
      { n: 2, text_ar: "صعب", low_confidence: true },
    ]);
    expect(result.pages_note).toBe(note + lowNote);
    expect(result.pdf_url).toBe("https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=9000&type=list");
  });
  it("uses the base API wording for readable pages", async () => {
    await createPages();
    await env.DB.prepare("INSERT INTO ruling_page VALUES (9000,1,'نص',0)").run();
    const result = await ruling();
    expect(result.pages).toEqual([{ n: 1, text_ar: "نص" }]);
    expect(result.pages_note).toBe(note);
  });
  it("omits pages and their note when none have been read", async () => {
    await createPages();
    const result = await ruling();
    expect(result.pages).toBeUndefined();
    expect(result.pages_note).toBeUndefined();
  });
  it("works with an older snapshot without the page table", async () => {
    const result = await ruling();
    expect(result.pages).toBeUndefined();
    expect(result.pages_note).toBeUndefined();
    expect(result.text_ar).toContain("قانون الاختبار");
    expect(result.pdf_url).toBeTruthy();
  });
});
