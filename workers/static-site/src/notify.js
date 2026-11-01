// POST /api/notify: the "Tell me if this changes" sign-up on /ai/, answered by
// the Worker so the static site does not depend on the origin.
//
// Who may post: only the site's own form in a browser. The request must carry
// the site's Origin and the browser's Sec-Fetch headers saying it is a
// same-origin form submission. That stops other websites and plain curl. It
// does not stop a script that forges those headers, so the limits below are
// what bound the damage: few tries per address per hour and per day, and a cap
// on all sign-ups per hour.
//
// The answer is a redirect to /ai/#notify-ok, #notify-bad or #notify-limit
// (a fragment, so the page stays a static file and works without JavaScript).

// Keep equal to NotifyConsent in internal/site/notify.go; a test compares them.
export const CONSENT = "Email me once if Lebanese Legislation starts answering questions about Lebanese law with AI. " +
  "Use my address for nothing else; I can ask for it to be deleted at any time.";

export const LIMITS = { perIpHour: 5, perIpDay: 10, allHour: 300 };
const HOUR = 3600 * 1000;
const DAY = 24 * HOUR;

const PATH = /^(?:\/(en|ar))?\/api\/notify$/;
const EMAIL = /^[^\s@<>()[\]\\,;:"]+@[^\s@<>()[\]\\,;:"]+\.[^\s@<>()[\]\\,;:"]+$/;

export function isNotifyPath(pathname) {
  return PATH.test(pathname);
}

const plain = (status, text, extra = {}) =>
  new Response(text, { status, headers: { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store", ...extra } });

function back(pathname, outcome) {
  const lang = PATH.exec(pathname)[1];
  const base = lang === "en" ? "/en/ai/" : "/ai/";
  return new Response(null, { status: 303, headers: { Location: `${base}#notify-${outcome}`, "Cache-Control": "no-store" } });
}

async function ipHash(env, ip, day) {
  const data = new TextEncoder().encode(`${env.NOTIFY_SALT || "lebleg-notify"}|${day}|${ip}`);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

// The storage the handler needs, over a D1 database.
export function d1Store(db) {
  return {
    async count(since, hash) {
      const row = hash
        ? await db.prepare("SELECT COUNT(*) AS n FROM attempts WHERE ip_hash = ?1 AND at >= ?2").bind(hash, since).first()
        : await db.prepare("SELECT COUNT(*) AS n FROM attempts WHERE at >= ?1").bind(since).first();
      return row ? row.n : 0;
    },
    async attempt(hash, at) {
      await db.prepare("INSERT INTO attempts (ip_hash, at) VALUES (?1, ?2)").bind(hash, at).run();
    },
    async prune(before) {
      await db.prepare("DELETE FROM attempts WHERE at < ?1").bind(before).run();
    },
    async add(email, createdAt, source, consent) {
      // a second sign-up with the same address is not an error and not stored twice
      await db.prepare("INSERT OR IGNORE INTO signups (email, created_at, source, consent) VALUES (?1, ?2, ?3, ?4)")
        .bind(email, createdAt, source, consent).run();
    },
  };
}

export async function handleNotify(request, env, store = env.NOTIFY_DB && d1Store(env.NOTIFY_DB), now = Date.now()) {
  const url = new URL(request.url);
  const allowed = (env.NOTIFY_ORIGINS || "https://lebaneselegislation.com").split(",").map((s) => s.trim());
  const origin = request.headers.get("Origin");
  const site = request.headers.get("Sec-Fetch-Site");
  const mode = request.headers.get("Sec-Fetch-Mode");
  if (!origin || !allowed.includes(origin) || site !== "same-origin" || (mode && mode !== "navigate")) {
    return plain(403, "Sign-ups only work from the form on https://lebaneselegislation.com/ai/. " +
      "If you are using that form and see this, write to nizar@taktek.io.");
  }
  if (!/^application\/x-www-form-urlencoded/i.test(request.headers.get("Content-Type") || "")) {
    return back(url.pathname, "bad");
  }
  if (Number(request.headers.get("Content-Length") || 0) > 2048) return back(url.pathname, "bad");
  const raw = await request.text();
  if (raw.length > 2048) return back(url.pathname, "bad");
  const form = new URLSearchParams(raw);

  if (form.get("website")) return back(url.pathname, "ok"); // the hidden field only robots fill in

  const ip = request.headers.get("CF-Connecting-IP") || "unknown";
  const hash = await ipHash(env, ip, new Date(now).toISOString().slice(0, 10));
  if ((await store.count(now - HOUR, hash)) >= LIMITS.perIpHour ||
      (await store.count(now - DAY, hash)) >= LIMITS.perIpDay ||
      (await store.count(now - HOUR)) >= LIMITS.allHour) {
    return back(url.pathname, "limit");
  }
  await store.attempt(hash, now); // every try counts, valid or not
  if (Math.random() < 0.05) await store.prune(now - 2 * DAY);

  const email = (form.get("email") || "").trim();
  if (!EMAIL.test(email) || email !== (form.get("email") || "") || email.length > 200 || form.get("consent") !== "yes") {
    return back(url.pathname, "bad");
  }
  const source = (form.get("from") || "").slice(0, 40);
  await store.add(email, new Date(now).toISOString(), source, CONSENT);
  return back(url.pathname, "ok");
}
