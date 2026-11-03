// Pages stored as their own parts (internal/staticexport/edgelayout.go), put
// back into the shared frame of their language when they are answered. The
// frame holds the header, footer, menu and analytics tags, so changing them
// is one new frame object in R2 instead of a new copy of every page.
//
// A page in parts is one line of ASCII, then the parts' bytes:
//
//   LLP1 <lang> <schema> <len>,<len>,...\n<part><part>...
//
// A frame (_layout/<id>.json) is {lang, schema, slots, text}: text[0], part 0,
// text[1], ... text[n] is the page. Everything is joined as bytes, so the page
// is the one the exporter rendered, byte for byte.

const MAGIC = "LLP1 ";
const encoder = new TextEncoder();
const decoder = new TextDecoder();

// isParts says whether a stored object is a page in parts. No HTML page
// starts with the magic.
export function isParts(bytes) {
  if (bytes.length < MAGIC.length) return false;
  for (let i = 0; i < MAGIC.length; i++) {
    if (bytes[i] !== MAGIC.charCodeAt(i)) return false;
  }
  return true;
}

// parseParts reads the header: the frame it belongs in (lang/schema) and
// where each part lies in bytes.
export function parseParts(bytes) {
  const nl = bytes.indexOf(10);
  if (nl < 0) throw new Error("page in parts: no header line");
  const fields = decoder.decode(bytes.subarray(MAGIC.length, nl)).split(" ");
  if (fields.length !== 3) throw new Error("page in parts: bad header");
  const lens = fields[2].split(",").map((s) => {
    if (!/^[0-9]+$/.test(s)) throw new Error("page in parts: bad length");
    return Number(s);
  });
  let at = nl + 1;
  const parts = lens.map((n) => {
    const p = bytes.subarray(at, at + n);
    if (p.length !== n) throw new Error("page in parts: truncated");
    at += n;
    return p;
  });
  if (at !== bytes.length) throw new Error("page in parts: bytes after the last part");
  return { key: fields[0] + "/" + fields[1], parts };
}

// prepareFrame turns a frame object into the bytes it is joined with.
export function prepareFrame(obj) {
  if (!obj || !Array.isArray(obj.text) || !Array.isArray(obj.slots) || obj.text.length !== obj.slots.length + 1) {
    throw new Error("frame: text and slots do not match");
  }
  return { key: obj.lang + "/" + obj.schema, text: obj.text.map((t) => encoder.encode(t)) };
}

// assemble joins a page's parts into its frame.
export function assemble(frame, page) {
  if (frame.key !== page.key || frame.text.length !== page.parts.length + 1) {
    throw new Error(`page in parts ${page.key} does not fit frame ${frame.key}`);
  }
  let n = 0;
  for (const t of frame.text) n += t.length;
  for (const p of page.parts) n += p.length;
  const out = new Uint8Array(n);
  let at = 0;
  for (let i = 0; i < page.parts.length; i++) {
    out.set(frame.text[i], at);
    at += frame.text[i].length;
    out.set(page.parts[i], at);
    at += page.parts[i].length;
  }
  out.set(frame.text[page.parts.length], at);
  return out;
}

// indexPointer reads _export/layout.json: the frame id of each lang/schema.
export function indexPointer(obj) {
  const m = new Map();
  for (const r of (obj && obj.layouts) || []) {
    if (r && typeof r.key === "string" && typeof r.id === "string" && !m.has(r.key)) m.set(r.key, r.id);
  }
  return m;
}
