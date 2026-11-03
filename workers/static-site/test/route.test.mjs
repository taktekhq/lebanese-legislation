import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { route, keyFor, resolve, indexRedirectData, hasRealQuery } from "../src/route.js";

const cases = JSON.parse(readFileSync(new URL("../../../internal/staticexport/testdata/resolve-cases.json", import.meta.url)));

test("resolve gives the same answers as Resolve in Go", async () => {
  const data = indexRedirectData(cases);
  const pages = new Set(cases.pages);
  const exists = async (p) => pages.has(p);
  for (const [from, want] of cases.cases) {
    assert.equal(await resolve(from, data, exists), want, from);
  }
});

test("only GET and HEAD for a page without a real query are served from files", () => {
  assert.equal(route("GET", "/law/1/", ""), "static");
  assert.equal(route("HEAD", "/en/decree/2/article/3/", ""), "static");
  assert.equal(route("POST", "/search/", ""), "origin");
  assert.equal(route("GET", "/scan/2000000001/page-1.png", ""), "static");
  assert.equal(route("HEAD", "/scan/2000000001/source.pdf", ""), "static");
  assert.equal(route("GET", "/law/1/", "?utm_source=chatgpt.com"), "static", "ChatGPT referrals stay off the origin");
  assert.equal(route("GET", "/law/1/", "?utm_source=chatgpt.com&gclid=x"), "static");
  assert.equal(route("GET", "/laws/", "?kind=decree"), "origin", "a filter is built by the origin");
  assert.equal(route("GET", "/search/", "?q=labour"), "origin", "a search with a query is answered by the server page (issue 43)");
  assert.equal(route("GET", "/law/1/go/", "?n=5"), "origin");
  for (const p of ["/api/v1/search/", "/ph/e/", "/ph", "/mcp", "/healthz", "/.well-known/x", "/ul/uploads/images/a.png",
    "/ruling-files/9.pdf", "/official/", "/en/official/3/", "/official-files/1/a"]) {
    assert.equal(route("GET", p, ""), "origin", p);
  }
  assert.equal(route("GET", "/phrases/", ""), "static", "only /ph and /ph/ are the analytics proxy");
  assert.equal(hasRealQuery("?utm_medium=a&fbclid=b"), false);
  assert.equal(hasRealQuery("?page=2"), true);
});

test("the OpenAPI description is a file from the bucket, like llms.txt", () => {
  for (const p of ["/openapi.json", "/llms.txt", "/robots.txt"]) {
    assert.equal(route("GET", p, ""), "static", p);
  }
  assert.equal(keyFor("/openapi.json"), "openapi.json");
});

test("keys: pages are index.html, files keep their name, _export is private", () => {
  assert.equal(keyFor("/"), "index.html");
  assert.equal(keyFor("/law/12/"), "law/12/index.html");
  assert.equal(keyFor("/decree/2/article/1-%d8%a7/"), "decree/2/article/1-%D8%A7/index.html");
  assert.equal(keyFor("/sitemap.xml"), "sitemap.xml");
  assert.equal(keyFor("/en/"), "en/index.html");
  assert.equal(keyFor("/_export/manifest.jsonl"), null);
  assert.equal(keyFor("/a/../b/"), null);
});
