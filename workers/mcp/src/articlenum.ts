// Article numbers have one spelling, the Centre's: "73", "12-1", "12 مكرر",
// "14 مكرر 2", "وحيدة", "1 اصدار". Callers (and citations) write "12/1",
// "19.1", "٥٨٦ (1)", "12 bis", "الوحيدة", "3,8", "90 وما يليها", "103 جديدة".
// numberForms folds them; it is the TypeScript twin of amend.Canon and
// amend.Resolve (internal/amend/canon.go), and both are tested against the
// one table in testdata/article_numbers.json.

const ARABIC_TO_ASCII = (s: string) =>
  s.replace(/[٠-٩]/g, (d) => String(d.charCodeAt(0) - 0x0660)).replace(/[۰-۹]/g, (d) => String(d.charCodeAt(0) - 0x06f0));

// foldAr folds Arabic the way amend.Fold does: no tashkeel or tatweel,
// أ إ آ ٱ → ا, ى → ي, ة → ه, digits to ASCII, dashes to "-", "،" to ",".
function foldAr(s: string): string {
  return ARABIC_TO_ASCII(s)
    .replace(/[ً-ٰٟـ­​-‏⁦-⁩﻿]/g, "")
    .replace(/[أإآٱ]/g, "ا").replace(/ى/g, "ي").replace(/ة/g, "ه")
    .replace(/[\u2013\u2014\u2212\u2010\u2011]/g, "-").replace(/،/g, ",")
    .replace(/\s+/g, " ").trim();
}

const repeat = (re: RegExp, s: string, to: string): string => {
  for (;;) {
    const t = s.replace(re, to);
    if (t === s) return s;
    s = t;
  }
};

const stripZeros = (n: string) => n.replace(/^0+/, "");

// The word after a number that makes it another article (ter, quater and
// on; amend.canonOtherArt): such a number is never taken for plain N.
const OTHER_ART = /^(?:مكرر|ثان|ثاني|ثالث|ثلاث|رابع|رباع|خامس|خماس|سادس|سداس|سابع|سباع|ثامن|ثمان|تاسع|تساع|عاشر|عشار)[اهة]?$/;

// readOne is the numbers one folded number may be, best first: the
// Centre's own spelling, then the others it falls back to (a sub-article
// the text lacks is its article).
function readOne(p: string): string[] {
  const out: string[] = [p];
  // "19.1", "12/1", "586 (1)", "6 - 2", "54 6" are all "N-M".
  let gap = repeat(/(\d) ?[./] ?(\d)/g, p, "$1-$2");
  gap = repeat(/(\d) ?\( ?(\d+) ?\)/g, gap, "$1-$2");
  gap = repeat(/(\d) ?- ?(\d)/g, gap, "$1-$2");
  gap = repeat(/(\d) (\d)/g, gap, "$1-$2");
  let m: RegExpMatchArray | null;
  if (/^(?:ال)?وحيده?$/.test(gap) || gap === "الوحيد") {
    out.push("وحيدة");
  } else if ((m = gap.match(/^(\d+) ?جديده$/))) {
    const n = stripZeros(m[1]);
    if (n) out.push(`${n} جديدة`, n);
  } else if ((m = gap.match(/^(\d+)$/))) {
    if (stripZeros(m[1])) out.push(stripZeros(m[1]));
  } else if ((m = gap.match(/^(\d+) مكرر(?: (\d+))?$/))) {
    const n = stripZeros(m[1]);
    if (n) {
      if (m[2]) out.push(`${n} مكرر ${stripZeros(m[2])}`, `${n} مكرر`);
      else out.push(`${n} مكرر`);
    }
  } else if ((m = gap.match(/^(\d+) اصدار$/))) {
    out.push(`${stripZeros(m[1])} اصدار`);
  } else if (/^\d+(?:-\d+)+$/.test(gap)) {
    // "42.02" is a tariff heading, not article 42-2: the Centre never
    // writes a sub-number with a leading zero.
    if (gap.split("-").slice(1).some((s) => s.length > 1 && s[0] === "0")) return out;
    const segs = gap.split("-").map((s) => stripZeros(s) || "0");
    for (let k = segs.length; k >= 1; k--) out.push(segs.slice(0, k).join("-"));
  } else if ((m = p.match(/^\d+/))) {
    // a number then words we do not read ("4 E", "2م"): the article, unless
    // the first word makes it another article ("267 رباعا", "6 مكرر ثان")
    if (OTHER_ART.test(p.slice(m[0].length).trim().split(" ")[0] ?? "")) return out;
    const n = stripZeros(m[0]);
    if (n) out.push(n);
  } else {
    const n = ordinal(p);
    if (n) out.push(String(n));
  }
  return out;
}

const UNITS: Record<string, number> = {
  اولي: 1, اول: 1, حاديه: 1, حادي: 1, واحده: 1, واحد: 1, ثانيه: 2, ثاني: 2, ثالثه: 3, ثالث: 3, رابعه: 4, رابع: 4,
  خامسه: 5, خامس: 5, سادسه: 6, سادس: 6, سابعه: 7, سابع: 7, ثامنه: 8, ثامن: 8, تاسعه: 9, تاسع: 9, عاشره: 10, عاشر: 10,
};
const TENS: Record<string, number> = {
  عشرون: 20, عشرين: 20, ثلاثون: 30, ثلاثين: 30, اربعون: 40, اربعين: 40, خمسون: 50, خمسين: 50,
  ستون: 60, ستين: 60, سبعون: 70, سبعين: 70, ثمانون: 80, ثمانين: 80, تسعون: 90, تسعين: 90,
};

// ordinal reads the words of an ordinal article number ("الثالثه", "الحاديه
// عشره", "الثالثه والسبعون") as amend.ArticleNumber does, up to 99; 0 when
// the words are not one from start to end.
function ordinal(s: string): number {
  const w = (t: string) => ({ and: t.startsWith("و") && !t.startsWith("واحد"), word: (t.startsWith("و") && !t.startsWith("واحد") ? t.slice(1) : t).replace(/^ال/, "") });
  const toks = s.split(" ");
  const first = w(toks[0]);
  if (first.and) return 0;
  if (toks.length === 1 && (first.word === "وحيده" || first.word === "وحيد")) return 0; // the sole article is "وحيدة", not 1
  if (TENS[first.word] && toks.length === 1) return TENS[first.word];
  const u = UNITS[first.word];
  if (!u) return 0;
  if (toks.length === 1) return u;
  const next = w(toks[1]);
  if (toks.length === 2 && !next.and && (next.word === "عشره" || next.word === "عشر") && u < 10) return u + 10;
  if (toks.length === 2 && next.and && TENS[next.word] && u < 10) return u + TENS[next.word];
  return 0;
}

function readAll(raw: string): string[][] {
  let s = foldAr(raw);
  s = s.replace(/^(?:رقم )?(?:ال)?ماد[هة] ?/, "");
  const lead = s.match(/^\((\d+)\)/);
  if (lead) s = lead[1] + s.slice(lead[0].length);
  if (s.length > 2 && s.startsWith("(") && s.endsWith(")")) s = s.slice(1, -1);
  s = s.replace(/^من /, "").replace(/ ?و?ما (?:يليها|يليهما|بعدها|بعدهما)$/, "");
  s = s.replace(/\bbis\b|مكرر[هةا]/gi, "مكرر")
    .replace(/(\d) ?- ?مكرر/g, "$1 مكرر").replace(/مكرر ?- ?(\d)/g, "مكرر $1").replace(/مكرر ?(\d)/g, "مكرر $1").trim();
  const range = s.match(/^(\d+) (?:الي|حتي|لغايه) (\d+)$/);
  if (range && +range[2] > +range[1] && +range[2] - +range[1] <= 300) {
    return [readOne(range[1]), readOne(range[2])]; // the ends; a lookup takes the first that exists
  }
  s = repeat(/(\d) ?و ?(\d)/g, s, "$1,$2");
  return s.split(",").map((x) => x.trim()).filter(Boolean).map(readOne);
}

// numberForms is what a caller may type for an article number, in the
// order to try: as given, then each number it names (a list names several)
// as the database writes it, with the fall-backs of amend.Resolve: "19.2"
// is "19-2", then "19"; "103 جديدة" is "103 جديدة" if the text has it, else
// "103"; "586 (1)", "586/1", "586 - 1", "٥٨٦-١" and "586 1" are "586-1";
// "12 bis" is "12 مكرر".
export function numberForms(input: string): string[] {
  const given = input.trim();
  const out = [given];
  for (const forms of readAll(given)) out.push(...forms);
  return [...new Set(out.filter(Boolean))];
}
