-- Sign-ups for "Tell me if this changes" on /ai/. The Worker only ever inserts.
-- Nothing in the code reads a sign-up back out or deletes one; a removal on
-- request is done by hand by the owner (see README).
CREATE TABLE IF NOT EXISTS signups (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  email      TEXT NOT NULL,
  created_at TEXT NOT NULL,   -- ISO 8601, UTC
  source     TEXT NOT NULL,   -- the form's "from" field, at most 40 characters
  consent    TEXT NOT NULL    -- the exact wording the person agreed to
);
CREATE UNIQUE INDEX IF NOT EXISTS signups_email ON signups (lower(email));

-- Rate limiting. ip_hash is a salted hash that changes every day, so rows
-- cannot be tied to a person across days. Old rows are pruned by the Worker.
CREATE TABLE IF NOT EXISTS attempts (
  ip_hash TEXT NOT NULL,
  at      INTEGER NOT NULL    -- milliseconds since the epoch
);
CREATE INDEX IF NOT EXISTS attempts_ip ON attempts (ip_hash, at);
CREATE INDEX IF NOT EXISTS attempts_at ON attempts (at);
