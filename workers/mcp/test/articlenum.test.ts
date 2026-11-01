// The spellings of an article number get_article accepts, against the table
// the Go side reads too (testdata/article_numbers.json: amend.Canon and the
// site's article lookup), so the two cannot drift.

import { describe, expect, it } from "vitest";
import table from "../../../testdata/article_numbers.json";
import { numberForms } from "../src/articlenum";

describe("numberForms", () => {
  it("has the table", () => {
    expect(table.cases.length).toBeGreaterThanOrEqual(40);
  });

  for (const c of table.cases) {
    it(`${c.raw} in [${c.target.join(" | ")}]`, () => {
      const has = new Set(c.target);
      const wanted = c.want.find((w) => has.has(w));
      const got = numberForms(c.raw).find((f) => has.has(f));
      expect(got).toBe(wanted);
    });
  }

  it("keeps what was typed first, folds the rest", () => {
    expect(numberForms("19.1")).toEqual(["19.1", "19-1", "19"]);
    expect(numberForms(" ٥٨٦ (1) ")[0]).toBe("٥٨٦ (1)");
    expect(numberForms("12 bis")).toContain("12 مكرر");
    expect(numberForms("المادة 12")).toContain("12");
  });
});
