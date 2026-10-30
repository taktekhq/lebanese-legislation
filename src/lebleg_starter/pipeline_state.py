"""Content identity and a durable, single-worker reconciliation ledger."""

import fcntl
import hashlib
import json
import os
import re
import sqlite3
import time
from contextlib import contextmanager
from pathlib import Path

STAGES = ('prepare', 'index', 'validate', 'export', 'upload', 'check', 'promote', 'metadata')
_DATA = re.compile(r'^(?:(?:data|metadata|annotations)/[^/]+\.(?:parquet|jsonl)|pdfs/[^/]+\.tar|schema/[^/]+\.(?:sql|json)|schema\.json)$')


def content_fingerprint(files):
    selected = []
    seen = set()
    for row in files:
        path = row['path']
        if '..' in path.split('/') or path.startswith('/') or '\\' in path:
            raise ValueError('invalid dataset path')
        if not _DATA.fullmatch(path):
            continue
        oid = (row.get('lfs') or {}).get('sha256') or (row.get('lfs') or {}).get('oid') or row.get('oid')
        if not oid or path in seen or type(row.get('size')) is not int or row['size'] < 0:
            raise ValueError('invalid content identity')
        seen.add(path)
        selected.append((path, oid, row['size']))
    return hashlib.sha256(json.dumps(sorted(selected), separators=(',', ':')).encode()).hexdigest()


@contextmanager
def worker_lock(path):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(path, os.O_CREAT | os.O_RDWR | getattr(os, 'O_NOFOLLOW', 0), 0o600)
    with os.fdopen(fd, 'w') as handle:
        try:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            yield False
            return
        try:
            yield True
        finally:
            fcntl.flock(handle, fcntl.LOCK_UN)


class Ledger:
    def __init__(self, path):
        path = Path(path)
        path.parent.mkdir(parents=True, exist_ok=True)
        if path.is_symlink():
            raise ValueError('invalid ledger path')
        self.path = path
        with self.connect() as db:
            db.executescript('''
                CREATE TABLE IF NOT EXISTS sources(repo TEXT PRIMARY KEY,revision TEXT NOT NULL,fingerprint TEXT NOT NULL,job INTEGER);
                CREATE TABLE IF NOT EXISTS jobs(id INTEGER PRIMARY KEY,repo TEXT NOT NULL,revision TEXT NOT NULL,
                    fingerprint TEXT NOT NULL,release_id TEXT NOT NULL,status TEXT NOT NULL,cursor INTEGER NOT NULL DEFAULT 0,
                    attempts INTEGER NOT NULL DEFAULT 0,retry_at REAL NOT NULL DEFAULT 0,error TEXT,
                    UNIQUE(repo,revision,fingerprint));
                CREATE TABLE IF NOT EXISTS receipts(job INTEGER,stage TEXT,value TEXT NOT NULL,PRIMARY KEY(job,stage));
                CREATE TABLE IF NOT EXISTS lanes(name TEXT PRIMARY KEY,repo TEXT NOT NULL,fingerprint TEXT NOT NULL,
                    required INTEGER NOT NULL,ok INTEGER NOT NULL);
                PRAGMA user_version=1;
            ''')
        path.chmod(0o600)

    def connect(self):
        db = sqlite3.connect(self.path, timeout=10)
        db.row_factory = sqlite3.Row
        return db

    @staticmethod
    def _identity(repo, revision, fingerprint):
        if not re.fullmatch(r'[\w.-]+/[\w.-]+', repo) or not re.fullmatch(r'[0-9a-f]{40}', revision) or not re.fullmatch(r'[0-9a-f]{64}', fingerprint):
            raise ValueError('invalid source identity')

    def observe(self, repo, revision, fingerprint, approved=False):
        self._identity(repo, revision, fingerprint)
        with self.connect() as db:
            db.execute('BEGIN IMMEDIATE')
            source = db.execute('SELECT * FROM sources WHERE repo=?', (repo,)).fetchone()
            if source and source['fingerprint'] == fingerprint:
                if approved and source['job']:
                    db.execute("UPDATE jobs SET status='pending' WHERE id=? AND status='reconcile'", (source['job'],))
                    return source['job']
                return None
            # Only coalesce jobs that have not started. Never supersede trusted work with an unapproved edit.
            db.execute("UPDATE jobs SET status='superseded' WHERE repo=? AND cursor=0 AND status=?",
                       (repo, 'pending' if approved else 'reconcile'))
            release = 'auto-' + revision[:12] + '-' + fingerprint[:12]
            row = db.execute('SELECT id FROM jobs WHERE repo=? AND revision=? AND fingerprint=?', (repo, revision, fingerprint)).fetchone()
            if row:
                job = row[0]
                db.execute('UPDATE jobs SET status=?,cursor=0,attempts=0,retry_at=0,error=NULL WHERE id=?',
                           ('pending' if approved else 'reconcile', job))
                db.execute('DELETE FROM receipts WHERE job=?', (job,))
            else:
                job = db.execute('INSERT INTO jobs(repo,revision,fingerprint,release_id,status) VALUES(?,?,?,?,?)',
                                 (repo, revision, fingerprint, release, 'pending' if approved else 'reconcile')).lastrowid
            db.execute('INSERT INTO sources VALUES(?,?,?,?) ON CONFLICT(repo) DO UPDATE SET revision=excluded.revision,fingerprint=excluded.fingerprint,job=excluded.job',
                       (repo, revision, fingerprint, job))
            return job

    def seed(self, repo, revision, fingerprint, release_id):
        self._identity(repo, revision, fingerprint)
        with self.connect() as db:
            db.execute('BEGIN IMMEDIATE')
            if db.execute('SELECT 1 FROM sources WHERE repo=?', (repo,)).fetchone():
                raise ValueError('source already initialized')
            job = db.execute("INSERT INTO jobs(repo,revision,fingerprint,release_id,status,cursor) VALUES(?,?,?,?, 'complete',?)",
                             (repo, revision, fingerprint, release_id, len(STAGES))).lastrowid
            db.execute('INSERT INTO sources VALUES(?,?,?,?)', (repo, revision, fingerprint, job))

    @staticmethod
    def _job(row):
        if row is None:
            return None
        value = dict(row)
        value['stage'] = STAGES[value['cursor']] if value['cursor'] < len(STAGES) else 'complete'
        return value

    def job(self, job):
        with self.connect() as db:
            return self._job(db.execute('SELECT * FROM jobs WHERE id=?', (job,)).fetchone())

    def next_job(self, now=None):
        with self.connect() as db:
            # A retried older job stays ahead of newer work, even during backoff.
            row = db.execute("SELECT * FROM jobs WHERE status IN ('pending','retry') ORDER BY id LIMIT 1").fetchone()
            if row and row['retry_at'] <= (time.time() if now is None else now):
                return self._job(row)
        return None

    def receipt(self, job, stage):
        with self.connect() as db:
            row = db.execute('SELECT value FROM receipts WHERE job=? AND stage=?', (job, stage)).fetchone()
            return json.loads(row[0]) if row else None

    def complete_stage(self, job, stage, receipt):
        raw = json.dumps(receipt, allow_nan=False)
        if len(raw) > 65536:
            raise ValueError('stage receipt too large')
        with self.connect() as db:
            db.execute('BEGIN IMMEDIATE')
            row = db.execute('SELECT cursor,status FROM jobs WHERE id=?', (job,)).fetchone()
            if not row or row['cursor'] >= len(STAGES) or STAGES[row['cursor']] != stage or row['status'] not in ('pending', 'retry'):
                raise ValueError('unexpected stage completion')
            db.execute('INSERT INTO receipts VALUES(?,?,?)', (job, stage, raw))
            cursor = row['cursor'] + 1
            db.execute('UPDATE jobs SET cursor=?,status=?,attempts=0,retry_at=0,error=NULL WHERE id=?',
                       (cursor, 'complete' if cursor == len(STAGES) else 'pending', job))

    def fail(self, job, reason='stage_failed', now=None, retry_after=None):
        # Exception strings can contain DSNs or signed URLs. Persist only controlled codes.
        allowed = {'stage_failed', 'budget_exhausted', 'disk_space', 'permission_denied', 'source_unapproved'}
        reason = reason if reason in allowed else 'stage_failed'
        with self.connect() as db:
            row = db.execute('SELECT attempts FROM jobs WHERE id=?', (job,)).fetchone()
            attempts = row[0] + 1
            delay = retry_after if retry_after is not None else min(3600, 60 * 2 ** min(attempts - 1, 6))
            db.execute('UPDATE jobs SET status=?,attempts=?,retry_at=?,error=? WHERE id=?',
                       ('failed' if attempts >= 10 else 'retry', attempts, (time.time() if now is None else now) + delay, reason, job))

    def retry(self, job):
        with self.connect() as db:
            db.execute("UPDATE jobs SET status='pending',attempts=0,retry_at=0,error=NULL WHERE id=? AND status IN ('retry','failed')", (job,))

    def lane(self, name, repo, fingerprint, required=True, ok=True):
        with self.connect() as db:
            db.execute('INSERT INTO lanes VALUES(?,?,?,?,?) ON CONFLICT(name) DO UPDATE SET repo=excluded.repo,fingerprint=excluded.fingerprint,required=excluded.required,ok=excluded.ok',
                       (name, repo, fingerprint, int(required), int(ok)))

    def status(self):
        with self.connect() as db:
            jobs = [self._job(row) for row in db.execute('SELECT * FROM jobs ORDER BY id')]
            sources = {row['repo']: dict(row) for row in db.execute('SELECT * FROM sources')}
            lanes = [dict(row) for row in db.execute('SELECT * FROM lanes')]
        current = all(j['status'] in ('complete', 'superseded') for j in jobs)
        for lane in lanes:
            lane['current'] = bool(lane['ok'] and lane['repo'] in sources and lane['fingerprint'] == sources[lane['repo']]['fingerprint'])
            if lane['required'] and not lane['current']:
                current = False
        return {'freshness': 'current' if current else 'partial', 'sources': list(sources.values()), 'jobs': jobs, 'lanes': lanes}
