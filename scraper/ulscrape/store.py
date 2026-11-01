"""Raw-first storage: verbatim HTML in content-addressed gzip blobs, with a
SQLite manifest carrying provenance and version history.

Nothing is parsed or rewritten -- what the server sent is what is kept. The
manifest records a *normalised* hash alongside each blob purely so that a
re-fetch of an unchanged page is recognised as unchanged (ViewState makes the
raw bytes differ every time) and does not create a duplicate blob.
"""
from __future__ import annotations

import gzip
import hashlib
import os
import sqlite3
import time
from pathlib import Path

SCHEMA = """
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;

-- one row per (type, id) ever probed, whether or not a record exists
CREATE TABLE IF NOT EXISTS doc (
  type         TEXT NOT NULL,
  doc_id       INTEGER NOT NULL,
  url          TEXT NOT NULL,
  present      INTEGER NOT NULL,      -- 1 record exists, 0 id is empty
  content_sha  TEXT,                  -- normalised hash of current version
  blob_sha     TEXT,                  -- sha256 of stored raw bytes
  raw_bytes    INTEGER,
  http_status  INTEGER,
  title        TEXT,
  first_seen   TEXT NOT NULL,
  last_checked TEXT NOT NULL,
  last_changed TEXT,
  n_versions   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (type, doc_id)
);
CREATE INDEX IF NOT EXISTS doc_present  ON doc(type, present);
CREATE INDEX IF NOT EXISTS doc_checked  ON doc(type, last_checked);

-- full history: every distinct content version ever observed
CREATE TABLE IF NOT EXISTS version (
  type        TEXT NOT NULL,
  doc_id      INTEGER NOT NULL,
  content_sha TEXT NOT NULL,
  blob_sha    TEXT NOT NULL,
  raw_bytes   INTEGER,
  first_seen  TEXT NOT NULL,
  last_seen   TEXT NOT NULL,
  PRIMARY KEY (type, doc_id, content_sha)
);

-- deduplicated raw payloads on disk
CREATE TABLE IF NOT EXISTS blob (
  blob_sha  TEXT PRIMARY KEY,
  path      TEXT NOT NULL,
  raw_bytes INTEGER NOT NULL,
  gz_bytes  INTEGER NOT NULL,
  stored_at TEXT NOT NULL
);

-- per-type scan bookkeeping so runs are resumable
CREATE TABLE IF NOT EXISTS scan (
  type         TEXT PRIMARY KEY,
  absent_sha   TEXT,                 -- baseline hash of the "no such id" page
  min_id       INTEGER,
  max_id       INTEGER,              -- highest id confirmed to exist
  ceiling      INTEGER,              -- highest id probed while bound-hunting
  cursor       INTEGER,              -- next id for the sequential sweep
  complete     INTEGER DEFAULT 0,
  updated      TEXT
);

-- verbatim copies of index/search pages (they carry the site's own counts)
CREATE TABLE IF NOT EXISTS page (
  url          TEXT PRIMARY KEY,
  content_sha  TEXT,
  blob_sha     TEXT,
  first_seen   TEXT,
  last_checked TEXT,
  last_changed TEXT
);

-- the site's advertised per-bucket totals, our completeness yardstick
CREATE TABLE IF NOT EXISTS advertised (
  type       TEXT NOT NULL,
  bucket     TEXT NOT NULL,          -- usually a year
  expected   INTEGER NOT NULL,
  captured_at TEXT NOT NULL,
  PRIMARY KEY (type, bucket, captured_at)
);

-- live id regions found by the coarse survey, so a resumed run does not
-- repeat the survey
CREATE TABLE IF NOT EXISTS region (
  type   TEXT NOT NULL,
  lo     INTEGER NOT NULL,
  hi     INTEGER NOT NULL,
  found  TEXT NOT NULL,
  PRIMARY KEY (type, lo)
);

-- pages reachable only through a parent's links (a law article's url needs the
-- law id), with the link text so chapter coverage can be judged
CREATE TABLE IF NOT EXISTS link (
  type      TEXT NOT NULL,
  doc_id    INTEGER NOT NULL,
  parent_id INTEGER NOT NULL,
  label     TEXT,
  PRIMARY KEY (type, doc_id)
);
CREATE INDEX IF NOT EXISTS link_parent ON link(type, parent_id);
-- blobs whose links are already in `link`
CREATE TABLE IF NOT EXISTS harvested (blob_sha TEXT PRIMARY KEY);

-- ids the site links to from its own index pages (completeness cross-check)
CREATE TABLE IF NOT EXISTS seed (
  type   TEXT NOT NULL,
  doc_id INTEGER NOT NULL,
  seen   TEXT NOT NULL,
  PRIMARY KEY (type, doc_id)
);

CREATE TABLE IF NOT EXISTS run (
  run_id    INTEGER PRIMARY KEY AUTOINCREMENT,
  mode      TEXT, started TEXT, finished TEXT,
  fetched   INTEGER DEFAULT 0, new_docs INTEGER DEFAULT 0,
  changed   INTEGER DEFAULT 0, errors  INTEGER DEFAULT 0,
  notes     TEXT
);
"""


def utcnow() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


class Store:
    def __init__(self, root: str | Path):
        self.root = Path(root)
        self.blobs = self.root / "blobs"
        self.blobs.mkdir(parents=True, exist_ok=True)
        self.db_path = self.root / "manifest.sqlite"
        self.db = sqlite3.connect(self.db_path, timeout=60,
                                  check_same_thread=False)
        self.db.executescript(SCHEMA)
        self.db.commit()

    # -- blobs ----------------------------------------------------------
    def put_blob(self, raw: bytes) -> str:
        """Store raw bytes gzipped, keyed by their own sha256. Idempotent."""
        blob_sha = hashlib.sha256(raw).hexdigest()
        rel = f"{blob_sha[:2]}/{blob_sha[2:4]}/{blob_sha}.html.gz"
        dest = self.blobs / rel
        if not dest.exists():
            dest.parent.mkdir(parents=True, exist_ok=True)
            tmp = dest.with_suffix(".tmp")
            # mtime=0 keeps the gzip container byte-reproducible
            with gzip.GzipFile(filename="", mode="wb", fileobj=open(tmp, "wb"),
                               compresslevel=9, mtime=0) as fh:
                fh.write(raw)
            os.replace(tmp, dest)
            self.db.execute(
                "INSERT OR IGNORE INTO blob VALUES (?,?,?,?,?)",
                (blob_sha, rel, len(raw), dest.stat().st_size, utcnow()))
        return blob_sha

    def read_blob(self, blob_sha: str) -> bytes:
        row = self.db.execute("SELECT path FROM blob WHERE blob_sha=?",
                              (blob_sha,)).fetchone()
        if not row:
            raise KeyError(blob_sha)
        with gzip.open(self.blobs / row[0], "rb") as fh:
            return fh.read()

    # -- documents ------------------------------------------------------
    def record(self, typ: str, doc_id: int, url: str, present: bool,
               raw: bytes | None, content_sha: str | None,
               http_status: int, title: str) -> str:
        """Upsert a probe result. Returns 'new' | 'changed' | 'same' | 'absent'."""
        now = utcnow()
        cur = self.db.execute(
            "SELECT present, content_sha, n_versions FROM doc WHERE type=? AND doc_id=?",
            (typ, doc_id)).fetchone()

        if not present:
            if cur is None:
                self.db.execute(
                    "INSERT INTO doc (type,doc_id,url,present,http_status,"
                    "first_seen,last_checked) VALUES (?,?,?,0,?,?,?)",
                    (typ, doc_id, url, http_status, now, now))
            else:
                self.db.execute(
                    "UPDATE doc SET last_checked=?, present=0 WHERE type=? AND doc_id=?",
                    (now, typ, doc_id))
            return "absent"

        blob_sha = self.put_blob(raw)
        outcome = "same"
        if cur is None:
            outcome = "new"
            self.db.execute(
                "INSERT INTO doc (type,doc_id,url,present,content_sha,blob_sha,"
                "raw_bytes,http_status,title,first_seen,last_checked,last_changed,"
                "n_versions) VALUES (?,?,?,1,?,?,?,?,?,?,?,?,1)",
                (typ, doc_id, url, content_sha, blob_sha, len(raw), http_status,
                 title, now, now, now))
        elif cur[1] != content_sha:
            outcome = "new" if cur[0] == 0 else "changed"
            self.db.execute(
                "UPDATE doc SET present=1, content_sha=?, blob_sha=?, raw_bytes=?,"
                " http_status=?, title=?, last_checked=?, last_changed=?,"
                " n_versions=n_versions+1 WHERE type=? AND doc_id=?",
                (content_sha, blob_sha, len(raw), http_status, title, now, now,
                 typ, doc_id))
        else:
            # back with the content it had before it went missing
            if cur[0] == 0:
                outcome = "new"
            self.db.execute(
                "UPDATE doc SET present=1, last_checked=?, http_status=? "
                "WHERE type=? AND doc_id=?",
                (now, http_status, typ, doc_id))

        self.db.execute(
            "INSERT INTO version (type,doc_id,content_sha,blob_sha,raw_bytes,"
            "first_seen,last_seen) VALUES (?,?,?,?,?,?,?) "
            "ON CONFLICT(type,doc_id,content_sha) DO UPDATE SET last_seen=excluded.last_seen",
            (typ, doc_id, content_sha, blob_sha, len(raw), now, now))
        return outcome

    def record_page(self, url: str, raw: bytes, content_sha: str) -> str:
        # An index page is never empty; an empty answer (a 404 the fetcher
        # passed on) keeps the copy we hold instead of blanking it.
        if not raw:
            return "empty"
        now = utcnow()
        blob_sha = self.put_blob(raw)
        cur = self.db.execute("SELECT content_sha FROM page WHERE url=?",
                              (url,)).fetchone()
        if cur is None:
            self.db.execute("INSERT INTO page VALUES (?,?,?,?,?,?)",
                            (url, content_sha, blob_sha, now, now, now))
            return "new"
        if cur[0] != content_sha:
            self.db.execute("UPDATE page SET content_sha=?, blob_sha=?,"
                            " last_checked=?, last_changed=? WHERE url=?",
                            (content_sha, blob_sha, now, now, url))
            return "changed"
        self.db.execute("UPDATE page SET last_checked=? WHERE url=?", (now, url))
        return "same"

    # -- scan state -----------------------------------------------------
    def scan_state(self, typ: str) -> dict:
        row = self.db.execute(
            "SELECT absent_sha,min_id,max_id,ceiling,cursor,complete FROM scan "
            "WHERE type=?", (typ,)).fetchone()
        keys = ("absent_sha", "min_id", "max_id", "ceiling", "cursor", "complete")
        return dict(zip(keys, row)) if row else {k: None for k in keys}

    def save_scan(self, typ: str, **kw) -> None:
        st = self.scan_state(typ)
        st.update({k: v for k, v in kw.items() if v is not None})
        self.db.execute(
            "INSERT INTO scan (type,absent_sha,min_id,max_id,ceiling,cursor,"
            "complete,updated) VALUES (?,?,?,?,?,?,?,?) "
            "ON CONFLICT(type) DO UPDATE SET absent_sha=excluded.absent_sha,"
            " min_id=excluded.min_id, max_id=excluded.max_id,"
            " ceiling=excluded.ceiling, cursor=excluded.cursor,"
            " complete=excluded.complete, updated=excluded.updated",
            (typ, st["absent_sha"], st["min_id"], st["max_id"], st["ceiling"],
             st["cursor"], st["complete"] or 0, utcnow()))
        self.db.commit()

    def save_regions(self, typ: str, regions) -> None:
        self.db.execute("DELETE FROM region WHERE type=?", (typ,))
        self.db.executemany(
            "INSERT OR REPLACE INTO region VALUES (?,?,?,?)",
            [(typ, int(a), int(b), utcnow()) for a, b in regions])
        self.commit()

    def load_regions(self, typ: str):
        return [(r[0], r[1]) for r in self.db.execute(
            "SELECT lo, hi FROM region WHERE type=? ORDER BY lo", (typ,))]

    def save_advertised(self, typ: str, buckets: dict) -> None:
        now = utcnow()
        self.db.executemany(
            "INSERT OR REPLACE INTO advertised VALUES (?,?,?,?)",
            [(typ, str(b), int(n), now) for b, n in buckets.items()])
        self.db.commit()

    # -- runs -----------------------------------------------------------
    def start_run(self, mode: str) -> int:
        # A killed run leaves its row open; close them out so `status` does not
        # show a row of phantom "running" runs. The single-instance lock means
        # any still-open row belongs to a process that is already gone.
        self.db.execute(
            "UPDATE run SET finished=?, notes=COALESCE(notes,'')||' [aborted]' "
            "WHERE finished IS NULL", (utcnow(),))
        cur = self.db.execute("INSERT INTO run (mode,started) VALUES (?,?)",
                              (mode, utcnow()))
        self.db.commit()
        return cur.lastrowid

    def finish_run(self, run_id: int, **kw) -> None:
        self.db.execute(
            "UPDATE run SET finished=?, fetched=?, new_docs=?, changed=?,"
            " errors=?, notes=? WHERE run_id=?",
            (utcnow(), kw.get("fetched", 0), kw.get("new_docs", 0),
             kw.get("changed", 0), kw.get("errors", 0), kw.get("notes", ""),
             run_id))
        self.db.commit()

    def commit(self) -> None:
        self.db.commit()

    def summary(self) -> list[tuple]:
        return self.db.execute(
            "SELECT d.type, SUM(d.present), COUNT(*),"
            "       COALESCE(SUM(d.raw_bytes),0), MAX(d.doc_id),"
            "       (SELECT complete FROM scan s WHERE s.type=d.type)"
            " FROM doc d GROUP BY d.type ORDER BY d.type").fetchall()
