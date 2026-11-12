// A page of the site as Markdown, for a request that prefers it
// (Accept: text/markdown). The Worker converts the HTML it already serves, so
// every static page has a Markdown version and none is stored twice.
//
// The words are the page's own, in its order: nothing is summarised, and no
// character of a legal text is escaped or changed. Only runs of ordinary
// white space (space, tab, line break) between words become one space, as a
// browser shows them; a no-break space stays a no-break space. Where a
// scanned text has two readings, both are kept, as the API gives them:
// ⟦one | two⟧. What is left out is what only a browser uses: the header,
// menu and footer, scripts, forms, buttons and decorative marks
// (aria-hidden). The footer's "not legal advice" notice is kept, as the last
// lines.

const VOID = new Set(["area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "source", "track", "wbr"]);
// Their content is not markup; the parser jumps to the closing tag.
const RAW = new Set(["script", "style", "textarea", "title"]);
// Left out with everything inside.
const SKIP = new Set(["script", "style", "svg", "form", "button", "noscript", "template", "select", "textarea", "input", "iframe", "dialog", "canvas", "audio", "video"]);
const BLOCK = new Set(["address", "article", "aside", "blockquote", "caption", "dd", "details", "div", "dl", "dt", "figcaption", "figure",
  "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "summary", "table",
  "tbody", "td", "tfoot", "th", "thead", "tr", "ul"]);

const NAMED = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: " ", middot: "·", larr: "←", rarr: "→",
  nearr: "↗", ldquo: "“", rdquo: "”", lsquo: "‘", rsquo: "’", hellip: "…", mdash: "—", ndash: "–",
  laquo: "«", raquo: "»", copy: "©", times: "×", bull: "•" };

export function decodeEntities(s) {
  if (!s.includes("&")) return s;
  return s.replace(/&(#x[0-9a-f]+|#[0-9]+|[a-z][a-z0-9]*);/gi, (m, e) => {
    if (e[0] === "#") {
      const n = e[1] === "x" || e[1] === "X" ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10);
      return n > 0 && n <= 0x10ffff ? String.fromCodePoint(n) : m;
    }
    if (Object.hasOwn(NAMED, e)) return NAMED[e];
    const lower = e.toLowerCase();
    return Object.hasOwn(NAMED, lower) ? NAMED[lower] : m;
  });
}

const ATTR = /([^\s=\/"'<>]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+)))?/g;

function attributes(src) {
  const a = Object.create(null);
  if (!src) return a;
  for (const m of src.matchAll(ATTR)) {
    const k = m[1].toLowerCase();
    if (!(k in a)) a[k] = decodeEntities(m[2] ?? m[3] ?? m[4] ?? "");
  }
  return a;
}

const TOKEN = /<!--[\s\S]*?-->|<![^>]*>|<(\/?)([a-zA-Z][a-zA-Z0-9-]*)((?:"[^"]*"|'[^']*'|[^>"'])*)>|[^<]+|</g;

// parse reads HTML into a tree of {tag, attrs, children} and {text}. It is
// lenient the way our own pages need: a closing tag closes every open tag
// inside it, and one that matches nothing is ignored.
export function parse(html) {
  const root = { tag: "#root", attrs: Object.create(null), children: [] };
  const stack = [root];
  TOKEN.lastIndex = 0;
  let m;
  while ((m = TOKEN.exec(html))) {
    const tok = m[0];
    const top = stack[stack.length - 1];
    if (tok[0] !== "<" || tok === "<") {
      top.children.push({ text: tok });
      continue;
    }
    if (!m[2]) continue; // a comment or the doctype
    const tag = m[2].toLowerCase();
    if (m[1]) {
      for (let i = stack.length - 1; i > 0; i--) {
        if (stack[i].tag === tag) {
          stack.length = i;
          break;
        }
      }
      continue;
    }
    const node = { tag, attrs: attributes(m[3]), children: [] };
    top.children.push(node);
    if (VOID.has(tag) || /\/\s*$/.test(m[3])) continue;
    if (RAW.has(tag)) {
      const closing = new RegExp("</" + tag, "ig");
      closing.lastIndex = TOKEN.lastIndex;
      const end = closing.exec(html)?.index ?? html.length;
      node.children.push({ text: html.slice(TOKEN.lastIndex, end), raw: true });
      const gt = html.indexOf(">", end);
      TOKEN.lastIndex = gt < 0 ? html.length : gt + 1;
      continue;
    }
    stack.push(node);
  }
  return root;
}

function find(node, test) {
  if (node.text !== undefined) return null;
  if (test(node)) return node;
  for (const c of node.children) {
    const hit = find(c, test);
    if (hit) return hit;
  }
  return null;
}

function hidden(n) {
  return SKIP.has(n.tag) || "hidden" in n.attrs || n.attrs["aria-hidden"] === "true";
}

const blockCache = new WeakMap();
function hasBlock(n) {
  if (n.text !== undefined) return false;
  let v = blockCache.get(n);
  if (v === undefined) {
    v = n.children.some((c) => c.text === undefined && !hidden(c) && (BLOCK.has(c.tag) || hasBlock(c)));
    blockCache.set(n, v);
  }
  return v;
}

function rawText(n) {
  if (n.text !== undefined) return n.raw ? n.text : decodeEntities(n.text);
  return n.children.map(rawText).join("");
}

// Ordinary white space: what HTML collapses. Not \s, which also matches the
// no-break spaces and U+FEFF that a French or Arabic text may carry.
const SPACES = /[ \t\n\r\f]+/g;
const strip = (s) => s.replace(/^[ \t\n\r\f]+|[ \t\n\r\f]+$/g, "");
const startsWithSpace = (s) => /^[ \t\n\r\f]/.test(s);
const endsWithSpace = (s) => /[ \t\n\r\f]$/.test(s);

// tidy makes a run of inline text one paragraph: spaces collapsed, a line per <br>.
function tidy(s) {
  return s.split("\n").map((l) => strip(l.replace(/[ \t\r\f]+/g, " "))).filter(Boolean).join("  \n");
}

function absolute(href, base) {
  try {
    return new URL(href, base).toString();
  } catch {
    return href;
  }
}

// linkTarget is an address that cannot end a Markdown link early: brackets
// and spaces are percent-encoded (encodeURIComponent leaves brackets alone).
function linkTarget(href, base) {
  return absolute(href, base).replace(/[()\s]/g, (c) => "%" + c.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0"));
}

function wrap(mark, s, close = mark) {
  const t = strip(s);
  if (!t) return "";
  return (startsWithSpace(s) ? " " : "") + mark + t + close + (endsWithSpace(s) ? " " : "");
}

// Two links with nothing between them (a row of filter links), or a card's
// bold title and the line after it inside one link, are set apart by the
// stylesheet; here, by a space. Nowhere else: "<bdi>46</bdi><bdi>/2021</bdi>"
// is the ruling number 46/2021.
function setApart(prev, n, ctx) {
  if (!prev) return false;
  if (prev.tag === "a" && n.tag === "a") return true;
  return ctx.inLink && (prev.tag === "b" || prev.tag === "strong");
}

function inline(nodes, ctx) {
  let out = "";
  let prev = null; // the element whose text was added last, when nothing came after it
  for (const n of nodes) {
    if (n.text !== undefined) {
      out += decodeEntities(n.text).replace(SPACES, " ");
      prev = null;
      continue;
    }
    if (hidden(n)) continue;
    const piece = inlineOne(n, ctx);
    if (!piece) continue;
    if (setApart(prev, n, ctx) && out && !endsWithSpace(out) && !startsWithSpace(piece)) out += " ";
    out += piece;
    prev = n;
  }
  return out;
}

function inlineOne(n, ctx) {
  switch (n.tag) {
    case "br":
      return "\n";
    case "a": {
      const text = inline(n.children, { ...ctx, inLink: true });
      const href = n.attrs.href || "";
      const label = strip(text.replace(SPACES, " "));
      if (!label) return "";
      if (!href || href.startsWith("#") || /^javascript:/i.test(href)) return text;
      return (startsWithSpace(text) ? " " : "") + "[" + label + "](" + linkTarget(href, ctx.base) + ")" + (endsWithSpace(text) ? " " : "");
    }
    case "strong":
    case "b":
      return wrap("**", inline(n.children, ctx));
    case "em":
    case "i":
      return wrap("*", inline(n.children, ctx));
    case "del":
    case "s":
      return wrap("~~", inline(n.children, ctx));
    case "sup":
    case "sub":
      return wrap("<" + n.tag + ">", inline(n.children, ctx), "</" + n.tag + ">");
    case "mark":
      // Two readings of a scanned word (marks in internal/site/scans.go):
      // both, in the brackets the API uses, so neither passes for the text.
      if (/(?:^|\s)unsure(?:\s|$)/.test(n.attrs.class || "")) {
        const readings = n.children.map((c) => (c.text === undefined && /unsure__or/.test(c.attrs.class || "") ? " | " : inline([c], ctx)));
        return "\u27e6" + strip(readings.join("")) + "\u27e7";
      }
      return inline(n.children, ctx);
    case "code":
    case "kbd":
    case "samp":
      return wrap("`", rawText(n));
    case "img":
      return n.attrs.alt && n.attrs.src ? "![" + n.attrs.alt.replace(SPACES, " ") + "](" + linkTarget(n.attrs.src, ctx.base) + ")" : "";
    default:
      // a block inside inline text (rare): its words, on the same line
      return hasBlock(n) || BLOCK.has(n.tag) ? " " + blocks([n], ctx).join(" ").replace(/\n+/g, " ") + " " : inline(n.children, ctx);
  }
}

function indent(s, pad) {
  return s.split("\n").map((l) => (l ? pad + l : l)).join("\n");
}

function list(n, ctx) {
  const ordered = n.tag === "ol";
  let i = Number(n.attrs.start) || 1;
  const items = [];
  for (const li of n.children) {
    if (li.text !== undefined || hidden(li)) continue;
    const parts = li.tag === "li" ? blocks(li.children, ctx) : blocks([li], ctx);
    if (!parts.length) continue;
    const mark = ordered ? i++ + ". " : "- ";
    items.push(mark + indent(parts.join("\n"), " ".repeat(mark.length)).slice(mark.length));
  }
  return items.join("\n");
}

function cell(n, ctx) {
  return strip(blocks(n.children, ctx).join(" ").replace(/[ \t]*\n[ \t]*/g, " ").replace(/\|/g, "\\|"));
}

function table(n, ctx) {
  const rows = [];
  const walk = (x) => {
    for (const c of x.children) {
      if (c.text !== undefined || hidden(c)) continue;
      if (c.tag === "tr") rows.push(c.children.filter((d) => d.tag === "td" || d.tag === "th").map((d) => cell(d, ctx)));
      else if (c.tag === "thead" || c.tag === "tbody" || c.tag === "tfoot") walk(c);
    }
  };
  walk(n);
  if (!rows.length) return [];
  const width = Math.max(...rows.map((r) => r.length));
  const line = (r) => "| " + Array.from({ length: width }, (_, i) => r[i] || "").join(" | ") + " |";
  const out = [];
  const caption = n.children.find((c) => c.tag === "caption");
  if (caption) out.push(...blocks(caption.children, ctx));
  out.push([line(rows[0]), "| " + Array(width).fill("---").join(" | ") + " |", ...rows.slice(1).map(line)].join("\n"));
  return out;
}

// blocks turns nodes into Markdown blocks (paragraphs, headings, lists...),
// which the caller joins with blank lines.
function blocks(nodes, ctx) {
  const out = [];
  let run = []; // inline nodes in a row: one paragraph
  const flush = () => {
    const t = run.length ? tidy(inline(run, ctx)) : "";
    if (t) out.push(t);
    run = [];
  };
  for (const n of nodes) {
    if (n.text !== undefined) {
      run.push(n);
      continue;
    }
    if (hidden(n)) continue;
    const t = n.tag;
    // A link stays a link even around a heading and a paragraph (a card).
    if (t === "a" || (!BLOCK.has(t) && !hasBlock(n))) {
      run.push(n);
      continue;
    }
    flush();
    if (/^h[1-6]$/.test(t)) {
      const text = tidy(inline(n.children, ctx)).replace(/ *\n/g, " ");
      if (text) out.push(ctx.inLink ? text : "#".repeat(Number(t[1])) + " " + text); // inside a link: its words only
    } else if (t === "ul" || t === "ol") {
      const l = list(n, ctx);
      if (l) out.push(l);
    } else if (t === "pre") {
      const code = find(n, (x) => x.tag === "code");
      const lang = /(?:^|\s)language-([\w-]+)/.exec(code?.attrs.class || "")?.[1] || "";
      const text = rawText(n).replace(/^\n+|[ \t\n\r\f]+$/g, "");
      // a fence longer than any run of backticks in the code
      const fence = "`".repeat(Math.max(3, ...(text.match(/`+/g) || []).map((r) => r.length + 1)));
      if (text) out.push(fence + lang + "\n" + text + "\n" + fence);
    } else if (t === "blockquote") {
      const inner = blocks(n.children, ctx).join("\n\n");
      if (inner) out.push(indent(inner, "> ").replace(/^$/gm, ">"));
    } else if (t === "table") {
      out.push(...table(n, ctx));
    } else if (t === "hr") {
      out.push("---");
    } else if (t === "dt" || t === "summary") {
      const text = hasBlock(n) ? "" : tidy(inline(n.children, ctx));
      if (text) out.push("**" + text + "**");
      else out.push(...blocks(n.children, ctx));
    } else {
      out.push(...blocks(n.children, ctx));
    }
  }
  flush();
  return out;
}

function yaml(s) {
  return JSON.stringify(strip(s.replace(SPACES, " ")));
}

// toMarkdown is the page at url as Markdown: a short front matter (title,
// description, address, language) and the content of <main>.
export function toMarkdown(html, url) {
  const root = parse(html);
  const ctx = { base: url };
  const htmlNode = find(root, (n) => n.tag === "html");
  const title = find(root, (n) => n.tag === "title");
  const meta = (name) => find(root, (n) => n.tag === "meta" && n.attrs.name === name)?.attrs.content || "";
  const canonical = find(root, (n) => n.tag === "link" && n.attrs.rel === "canonical")?.attrs.href || "";
  const front = ["---"];
  if (title) front.push("title: " + yaml(decodeEntities(rawText(title))));
  if (meta("description")) front.push("description: " + yaml(meta("description")));
  front.push("url: " + (canonical ? absolute(canonical, url) : url));
  if (htmlNode?.attrs.lang) front.push("lang: " + htmlNode.attrs.lang);
  front.push("---");
  const main = find(root, (n) => n.tag === "main") || find(root, (n) => n.tag === "body") || root;
  const body = blocks(main.children, ctx);
  const notice = find(root, (n) => /(?:^|\s)footer__notice(?:\s|$)/.test(n.attrs?.class || ""));
  if (notice && main.tag === "main") {
    const lines = blocks(notice.children, ctx);
    if (lines.length) body.push("---", ...lines);
  }
  return front.join("\n") + "\n\n" + body.join("\n\n") + "\n";
}
