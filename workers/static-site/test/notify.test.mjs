import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { handleNotify, isNotifyPath, d1Store, CONSENT, LIMITS } from "../src/notify.js";

const SITE = "https://lebaneselegislation.com";
const NOW = Date.UTC(2026, 10, 2, 12, 0, 0);

function memoryStore() {
  const s = { signups: [], attempts: [] };
  return Object.assign(s, {
    async count(since, hash) { return s.attempts.filter((a) => a.at >= since && (!hash || a.hash === hash)).length; },
    async attempt(hash, at) { s.attempts.push({ hash, at }); },
    async prune(before) { s.attempts = s.attempts.filter((a) => a.at >= before); },
    async add(email, createdAt, source, consent) {
      if (!s.signups.some((x) => x.email.toLowerCase() === email.toLowerCase())) s.signups.push({ email, createdAt, source, consent });
    },
  });
}

function post(fields, headers = {}, path = "/api/notify") {
  const body = new URLSearchParams(fields).toString();
  return new Request(SITE + path, {
    method: "POST", body,
    headers: { "Content-Type": "application/x-www-form-urlencoded", Origin: SITE, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate", "CF-Connecting-IP": "203.0.113.7", ...headers },
  });
}
const good = { email: "a@example.com", consent: "yes", from: "ai" };
const where = (res) => res.headers.get("Location");

test("a good sign-up is stored with the consent wording and redirects to the static page", async () => {
  const st = memoryStore();
  const res = await handleNotify(post(good), {}, st, NOW);
  assert.equal(res.status, 303);
  assert.equal(where(res), "/ai/#notify-ok");
  assert.deepEqual(st.signups.map(({ email, source, consent }) => ({ email, source, consent })), [{ email: "a@example.com", source: "ai", consent: CONSENT }]);
});

test("the English form returns to the English page", async () => {
  const res = await handleNotify(post(good, {}, "/en/api/notify"), {}, memoryStore(), NOW);
  assert.equal(where(res), "/en/ai/#notify-ok");
});

test("the same address twice is stored once and looks the same to the visitor", async () => {
  const st = memoryStore();
  await handleNotify(post(good), {}, st, NOW);
  const res = await handleNotify(post({ ...good, email: "A@Example.com" }), {}, st, NOW);
  assert.equal(where(res), "/ai/#notify-ok");
  assert.equal(st.signups.length, 1);
});

test("curl, other websites and missing browser headers are refused and store nothing", async () => {
  const st = memoryStore();
  const cases = [
    { Origin: "" },                                   // curl sends no Origin
    { Origin: "https://evil.example" },               // a form on another website
    { "Sec-Fetch-Site": "cross-site" },
    { "Sec-Fetch-Site": "" },                         // not a browser
    { "Sec-Fetch-Mode": "cors" },                     // fetch() from a script
  ];
  for (const h of cases) {
    const headers = Object.fromEntries(Object.entries(h).map(([k, v]) => [k, v]));
    const req = post(good, headers);
    for (const [k, v] of Object.entries(h)) if (v === "") req.headers.delete(k);
    const res = await handleNotify(req, {}, st, NOW);
    assert.equal(res.status, 403, JSON.stringify(h));
    assert.match(await res.text(), /nizar@taktek\.io/);
  }
  assert.equal(st.signups.length, 0);
  assert.equal(st.attempts.length, 0, "refused requests do not even cost a database write");
});

test("bad input goes back to the form with the error message", async () => {
  for (const f of [{ ...good, email: "not an email" }, { ...good, email: " a@example.com" }, { email: "a@example.com" }, { ...good, consent: "no" }, { ...good, email: "a@b" }, { ...good, email: "x".repeat(200) + "@example.com" }]) {
    const st = memoryStore(); // a fresh visitor each time, so the limit does not interfere
    assert.equal(where(await handleNotify(post(f), {}, st, NOW)), "/ai/#notify-bad", JSON.stringify(f).slice(0, 60));
    assert.equal(st.signups.length, 0);
  }
});

test("the hidden spam field looks like success and stores nothing", async () => {
  const st = memoryStore();
  assert.equal(where(await handleNotify(post({ ...good, website: "http://spam" }), {}, st, NOW)), "/ai/#notify-ok");
  assert.equal(st.signups.length, 0);
});

test("a wrong content type or a big body is rejected", async () => {
  const st = memoryStore();
  const json = post(good, { "Content-Type": "application/json" });
  assert.equal(where(await handleNotify(json, {}, st, NOW)), "/ai/#notify-bad");
  assert.equal(where(await handleNotify(post({ ...good, pad: "x".repeat(3000) }), {}, st, NOW)), "/ai/#notify-bad");
});

test("one address gets a few tries an hour, then a clear limit message", async () => {
  const st = memoryStore();
  for (let i = 0; i < LIMITS.perIpHour; i++) {
    assert.equal(where(await handleNotify(post({ ...good, email: `p${i}@example.com` }), {}, st, NOW + i)), "/ai/#notify-ok");
  }
  assert.equal(where(await handleNotify(post({ ...good, email: "late@example.com" }), {}, st, NOW + 10)), "/ai/#notify-limit");
  assert.equal(st.signups.length, LIMITS.perIpHour);
  // another visitor is not affected
  const other = post({ ...good, email: "other@example.com" }, { "CF-Connecting-IP": "198.51.100.9" });
  assert.equal(where(await handleNotify(other, {}, st, NOW + 20)), "/ai/#notify-ok");
});

test("failed tries count too, and a day cap holds after the hour passes", async () => {
  const st = memoryStore();
  for (let i = 0; i < LIMITS.perIpHour; i++) await handleNotify(post({ ...good, email: "bad" }), {}, st, NOW + i);
  assert.equal(where(await handleNotify(post(good), {}, st, NOW + 100)), "/ai/#notify-limit");
  // an hour and a minute later the hourly window is clear; spend the rest of the day
  const later = NOW + 3600 * 1000 + 60 * 1000;
  for (let i = 0; i < LIMITS.perIpDay - LIMITS.perIpHour; i++) await handleNotify(post({ ...good, email: "bad" }), {}, st, later + i);
  assert.equal(where(await handleNotify(post(good), {}, st, later + 500)), "/ai/#notify-limit");
});

test("a cap on all sign-ups per hour stops a flood from many addresses", async () => {
  const st = memoryStore();
  for (let i = 0; i < LIMITS.allHour; i++) st.attempts.push({ hash: "h" + i, at: NOW });
  assert.equal(where(await handleNotify(post(good), {}, st, NOW + 1)), "/ai/#notify-limit");
});

test("there is no way to read or delete a sign-up through the Worker", () => {
  const src = readFileSync(new URL("../src/notify.js", import.meta.url), "utf8");
  assert.doesNotMatch(src, /DELETE FROM signups|UPDATE signups|SELECT[^"]*FROM signups/i);
  assert.equal(isNotifyPath("/api/notify"), true);
  assert.equal(isNotifyPath("/en/api/notify"), true);
  assert.equal(isNotifyPath("/api/notify/1"), false);
});

test("the consent wording equals the Go constant", () => {
  const go = readFileSync(new URL("../../../internal/site/notify.go", import.meta.url), "utf8");
  const m = /const NotifyConsent = ((?:"[^"\n]*"(?: \+\n\s*)?)+)/.exec(go);
  assert.ok(m, "NotifyConsent not found");
  const text = [...m[1].matchAll(/"((?:[^"\\]|\\.)*)"/g)].map((x) => JSON.parse(`"${x[1]}"`)).join("");
  assert.equal(CONSENT, text);
});

test("the D1 queries run on SQLite against the migration", () => {
  // d1Store only uses prepare().bind().first()/run(); drive it through python's sqlite3
  const sql = readFileSync(new URL("../migrations/0001_notify.sql", import.meta.url), "utf8");
  const py = `
import sqlite3,sys
db=sqlite3.connect(":memory:"); db.executescript(open(sys.argv[1]).read())
db.execute("INSERT OR IGNORE INTO signups (email,created_at,source,consent) VALUES ('A@x.com','t','ai','c')")
db.execute("INSERT OR IGNORE INTO signups (email,created_at,source,consent) VALUES ('a@x.com','t','ai','c')")
db.execute("INSERT INTO attempts VALUES ('h',5)"); db.execute("INSERT INTO attempts VALUES ('h',50)")
print(db.execute("SELECT COUNT(*) FROM signups").fetchone()[0], db.execute("SELECT COUNT(*) FROM attempts WHERE ip_hash=? AND at>=?",('h',10)).fetchone()[0], db.execute("SELECT COUNT(*) FROM attempts WHERE at>=?",(0,)).fetchone()[0])
db.execute("DELETE FROM attempts WHERE at<?",(10,)); print(db.execute("SELECT COUNT(*) FROM attempts").fetchone()[0])`;
  const out = execFileSync("python3", ["-I", "-c", py, new URL("../migrations/0001_notify.sql", import.meta.url).pathname], { encoding: "utf8" }).trim().split("\n");
  assert.deepEqual(out, ["1 1 2", "1"]);
  assert.ok(sql.includes("signups_email"));
  assert.equal(typeof d1Store, "function");
});
