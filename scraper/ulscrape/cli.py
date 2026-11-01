"""Command line entry point.

    ul-scrape indexes    archive index pages, record the site's own totals
    ul-scrape backfill   sweep the id space for every type (resumable)
    ul-scrape daily      nightly delta: new ids + changed docs + rotating recheck
    ul-scrape status     progress against the site's advertised totals
    ul-scrape verify     re-probe everything the site links to but we lack
    ul-scrape rehash     offline: recompute presence + hashes from stored blobs
    ul-scrape articles   full text of every law, then each article page (resumable)
    ul-scrape export     mirror held pages into the git-tracked ../archive
"""
from __future__ import annotations

import argparse
import os
import sqlite3
import sys
import time
from pathlib import Path

from . import indexes, links
from .crawl import Budget, Crawler
from .net import Fetcher, content_hash, is_blank
from .store import Store, utcnow
from .targets import BASE, BASE_FALLBACK, TARGETS

ROOT = Path(__file__).resolve().parent.parent
DATA = Path(os.environ.get("UL_DATA", ROOT / "data"))
# Order matters: the sections the client cares about most land first, so the
# archive is useful long before the whole sweep finishes.
DEFAULT_TYPES = ["law", "ruling", "journal", "opinion", "agreement",
                 "article", "research", "news", "custompage"]


def make_logger(logdir: Path, quiet: bool = False):
    logdir.mkdir(parents=True, exist_ok=True)
    path = logdir / f"{time.strftime('%Y-%m-%d')}.log"
    fh = open(path, "a", buffering=1, encoding="utf-8")

    def log(msg: str) -> None:
        line = f"{utcnow()}  {msg}"
        fh.write(line + "\n")
        if not quiet:
            print(line, flush=True)
    return log


class Lock:
    """Crude single-instance guard so a 05:00 cron run cannot overlap a
    still-running backfill."""

    def __init__(self, path: Path):
        self.path = path

    def __enter__(self):
        if self.path.exists():
            try:
                pid = int(self.path.read_text().split()[0])
                os.kill(pid, 0)
            except (ValueError, ProcessLookupError, IndexError):
                self.path.unlink(missing_ok=True)   # stale
            except PermissionError:
                pass
            else:
                # Another ul-scrape job holds the shared lock: this run is a skip,
                # not a failure, so exit 0 and let launchd try again next slot.
                print(f"skip: already running (pid {pid}); lock {self.path}",
                      file=sys.stderr, flush=True)
                raise SystemExit(0)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.path.write_text(f"{os.getpid()} {utcnow()}\n")
        return self

    def __exit__(self, *exc):
        self.path.unlink(missing_ok=True)


def build(args):
    store = Store(DATA)
    log = make_logger(DATA / "logs", quiet=args.quiet)
    fetch = Fetcher(BASE, BASE_FALLBACK, delay=args.delay,
                    timeout=args.timeout, retries=args.retries, log=log)
    crawler = Crawler(store, fetch, workers=args.workers, log=log,
                      gap_tolerance=args.gap_tolerance,
                      stall_minutes=args.stall_minutes)
    return store, log, fetch, crawler


# ---------------------------------------------------------------- commands
def cmd_indexes(args):
    store, log, fetch, crawler = build(args)
    run = store.start_run("indexes")
    budget = Budget(args.minutes * 60 if args.minutes else None, args.max_requests)
    crawler.refresh_indexes(budget)
    seeded = crawler.harvest_year_pages(budget)
    log(f"[indexes] harvested ids: " +
        ", ".join(f"{t}={len(v):,}" for t, v in sorted(seeded.items())))
    _persist_seeds(store, seeded, log)
    store.finish_run(run, fetched=crawler.counters["fetched"],
                     errors=crawler.counters["errors"], notes="index refresh")
    return crawler


def _persist_seeds(store, seeded, log):
    """Remember ids the site links to, so `verify` can prove we hold them."""
    store.db.execute("CREATE TABLE IF NOT EXISTS seed ("
                     "type TEXT, doc_id INTEGER, seen TEXT,"
                     " PRIMARY KEY (type, doc_id))")
    now = utcnow()
    for typ, ids in seeded.items():
        store.db.executemany(
            "INSERT INTO seed VALUES (?,?,?) ON CONFLICT(type,doc_id) DO UPDATE "
            "SET seen=excluded.seen", [(typ, i, now) for i in ids])
    store.commit()


def _sweep_type(crawler, store, log, typ, budget, order):
    """Discover where the type's ids live, then probe every id inside them."""
    if store.scan_state(typ).get("complete"):
        # Cheap no-op so a supervisor can re-run `backfill` on a timer without
        # re-probing finished sections. Rechecking settled documents is the
        # nightly job's business, not the backfill's.
        log(f"[{typ}] already complete, skipping")
        return
    absent = crawler.calibrate(typ)
    st = store.scan_state(typ)
    ceiling = st.get("ceiling") or 0
    if not ceiling:
        ceiling = crawler.extend_ceiling(typ, absent, budget)
    if budget.exhausted:
        return
    regions = store.load_regions(typ)
    if not regions:
        regions = crawler.survey_regions(typ, absent, ceiling, budget)
    if budget.exhausted:
        return

    settled = {r[0] for r in store.db.execute(
        "SELECT doc_id FROM doc WHERE type=?", (typ,)).fetchall()}
    todo = [i for lo, hi in regions for i in range(lo, hi + 1)
            if i not in settled]
    todo.sort(reverse=(order == "desc"))
    log(f"[{typ}] {len(todo):,} ids left to probe ({len(settled):,} already "
        f"settled), newest first" if order == "desc" else
        f"[{typ}] {len(todo):,} ids left to probe")
    crawler.sweep(typ, todo, absent, budget)

    # the coarse survey can step over a short cluster; re-sample the dead space
    # at a finer interval and sweep anything new it turns up
    if not budget.exhausted:
        refined = crawler.refine_gaps(typ, absent, regions, ceiling, budget)
        if refined != regions:
            settled = {r[0] for r in store.db.execute(
                "SELECT doc_id FROM doc WHERE type=?", (typ,)).fetchall()}
            extra = [i for lo, hi in refined for i in range(lo, hi + 1)
                     if i not in settled]
            extra.sort(reverse=(order == "desc"))
            if extra:
                log(f"[{typ}] sweeping {len(extra):,} additional ids from refinement")
                crawler.sweep(typ, extra, absent, budget)

    if not budget.exhausted:
        top = store.db.execute(
            "SELECT MAX(doc_id) FROM doc WHERE type=? AND present=1",
            (typ,)).fetchone()[0] or 0
        store.save_scan(typ, complete=1, max_id=top)
        log(f"[{typ}] id space complete; {top:,} highest live id")


def cmd_backfill(args):
    store, log, fetch, crawler = build(args)
    run = store.start_run("backfill")
    budget = Budget(args.minutes * 60 if args.minutes else None, args.max_requests)
    if not args.skip_indexes:
        crawler.refresh_indexes(budget)
    for typ in args.types:
        if budget.exhausted:
            log("budget exhausted; stopping (state is saved, rerun to resume)")
            break
        _sweep_type(crawler, store, log, typ, budget, args.order)
    c = crawler.counters
    log(f"[backfill] fetched={c['fetched']:,} new={c['new']:,} "
        f"changed={c['changed']:,} absent={c['absent']:,} errors={c['errors']:,}")
    store.finish_run(run, fetched=c["fetched"], new_docs=c["new"],
                     changed=c["changed"], errors=c["errors"], notes="backfill")
    return crawler


def _harvest_links(store):
    """Record article/chapter links from law and chapter pages not yet read."""
    import gzip
    rows = store.db.execute(
        "SELECT DISTINCT b.blob_sha, b.path FROM doc d JOIN blob b ON b.blob_sha=d.blob_sha"
        " WHERE d.type IN ('law','lawsection') AND d.present=1"
        " AND d.blob_sha NOT IN (SELECT blob_sha FROM harvested)").fetchall()
    for blob_sha, rel in rows:
        _record_links(store, blob_sha, gzip.open(store.blobs / rel).read())
    store.commit()
    return len(rows)


def _record_links(store, blob_sha, raw):
    arts, secs = links.extract(raw)
    store.db.executemany("INSERT OR IGNORE INTO link VALUES ('lawarticle',?,?,?)", arts)
    store.db.executemany("INSERT OR IGNORE INTO link VALUES ('lawsection',?,?,?)", secs)
    store.db.execute("INSERT OR IGNORE INTO harvested VALUES (?)", (blob_sha,))


def _fetch_articles(crawler, store, log, budget):
    """Fetch every law article we hold a link to but not the text of.

    1. harvest links from every law page (and chapter page) not yet read;
    2. where a law page leaves some chapter's articles unlisted (the big codes),
       fetch its chapter pages in order, skipping a chapter once its article
       range is already known -- one chapter page often reveals a whole book;
    3. fetch the articles: those codes first, then newest laws first.
    """
    import concurrent.futures as cf
    lock, db = crawler._db_lock, store.db
    log(f"[articles] harvested links from {_harvest_links(store):,} pages")

    chapters: dict[int, list] = {}
    for sid, law, label in db.execute(
            "SELECT doc_id, parent_id, label FROM link WHERE type='lawsection' ORDER BY rowid"):
        chapters.setdefault(law, []).append((sid, label))

    def known(law):
        return {links.article_number(r[0]) for r in db.execute(
            "SELECT label FROM link WHERE type='lawarticle' AND parent_id=?", (law,))}

    with lock:
        codes = {law for law, secs in chapters.items()
                 if not all(links.covered(lbl, known(law)) for _, lbl in secs)}
    log(f"[articles] {len(codes):,} laws list chapters without all their articles")

    def walk(law):
        for sid, label in chapters[law]:
            if budget.exhausted:
                return
            with lock:
                if links.covered(label, known(law)):
                    continue
                row = db.execute("SELECT present, blob_sha FROM doc WHERE type='lawsection'"
                                 " AND doc_id=?", (sid,)).fetchone()
            if row is None:
                if not budget.take():
                    return
                crawler.probe("lawsection", sid, "")
                with lock:
                    row = db.execute("SELECT present, blob_sha FROM doc WHERE type='lawsection'"
                                     " AND doc_id=?", (sid,)).fetchone()
            if row and row[0] == 1:
                with lock:
                    if not db.execute("SELECT 1 FROM harvested WHERE blob_sha=?",
                                      (row[1],)).fetchone():
                        _record_links(store, row[1], store.read_blob(row[1]))
        with lock:
            store.commit()

    order = sorted(codes, reverse=True)
    with cf.ThreadPoolExecutor(crawler.workers) as pool:
        for n, _ in enumerate(pool.map(walk, order), 1):
            if n % 100 == 0:
                log(f"[articles] chapters: {n:,}/{len(order):,} laws walked")
            crawler._check_stall()
    if budget.exhausted:
        return

    held = {r[0] for r in db.execute("SELECT doc_id FROM doc WHERE type='lawarticle'")}
    rows = db.execute("SELECT doc_id, parent_id FROM link WHERE type='lawarticle'").fetchall()
    rows.sort(key=lambda r: (r[1] not in codes, -r[1], r[0]))
    todo = [a for a, _ in rows if a not in held]
    log(f"[articles] {len(todo):,} articles to fetch ({len(held):,} held, "
        f"{len(rows):,} linked); codes first, then newest laws")
    crawler.sweep("lawarticle", todo, "", budget)


def _fetch_lawtext(crawler, store, log, budget, first: int = 2000):
    """Fetch the full-text view of every law not yet held.

    The largest law pages (the codes and other long laws) go first, so the
    texts people use most land early; then newest first.
    """
    db = store.db
    held = {r[0] for r in db.execute("SELECT doc_id FROM doc WHERE type='lawtext'")}
    laws = db.execute("SELECT doc_id, raw_bytes FROM doc WHERE type='law' AND present=1"
                      " ORDER BY raw_bytes DESC").fetchall()
    big = [i for i, _ in laws[:first]]
    rest = sorted((i for i, _ in laws[first:]), reverse=True)
    todo = [i for i in big + rest if i not in held]
    log(f"[lawtext] {len(todo):,} full texts to fetch ({len(held):,} held); "
        f"largest {first:,} first, then newest")
    crawler.sweep("lawtext", todo, "", budget)


def cmd_articles(args):
    """Full text of every law, then each article's own page."""
    store, log, fetch, crawler = build(args)
    run = store.start_run("articles")
    budget = Budget(args.minutes * 60 if args.minutes else None, args.max_requests)
    _fetch_lawtext(crawler, store, log, budget)
    if not budget.exhausted:
        _fetch_articles(crawler, store, log, budget)
    c = crawler.counters
    log(f"[articles] fetched={c['fetched']:,} new={c['new']:,} changed={c['changed']:,} "
        f"absent={c['absent']:,} errors={c['errors']:,}")
    store.finish_run(run, fetched=c["fetched"], new_docs=c["new"], changed=c["changed"],
                     errors=c["errors"], notes="law articles")
    return crawler


def cmd_daily(args):
    """Nightly delta pass.

    1. re-archive the index pages and the per-year browse pages (cheap, and the
       only place the site reveals its own totals);
    2. fetch anything newly linked that we do not already hold;
    3. push each type's ceiling upward to catch brand-new records;
    4. if a backfill is still unfinished, continue it;
    5. spend whatever budget is left re-checking the least recently verified
       documents, so the whole corpus is revalidated on a rolling basis.
    """
    store, log, fetch, crawler = build(args)
    run = store.start_run("daily")
    budget = Budget(args.minutes * 60 if args.minutes else None, args.max_requests)

    crawler.refresh_indexes(budget)
    seeded = crawler.harvest_year_pages(budget)
    _persist_seeds(store, seeded, log)

    # 2. newly linked ids we have never probed
    for typ, ids in sorted(seeded.items()):
        if typ not in TARGETS or budget.exhausted:
            continue
        have = {r[0] for r in store.db.execute(
            "SELECT doc_id FROM doc WHERE type=?", (typ,)).fetchall()}
        fresh = sorted(ids - have, reverse=True)
        if fresh:
            log(f"[daily] {typ}: {len(fresh):,} newly linked ids")
            crawler.sweep(typ, fresh, crawler.calibrate(typ), budget)

    # 3. extend the ceiling for each type
    for typ in args.types:
        if budget.exhausted:
            break
        st = store.scan_state(typ)
        if not st.get("complete"):
            continue  # step 4 owns unfinished sections
        absent = crawler.calibrate(typ)
        top = store.db.execute(
            "SELECT MAX(doc_id) FROM doc WHERE type=? AND present=1",
            (typ,)).fetchone()[0]
        if top:
            tail = range(top + 1, top + 1 + args.tail)
            unprobed = [i for i in tail if not store.db.execute(
                "SELECT 1 FROM doc WHERE type=? AND doc_id=?", (typ, i)).fetchone()]
            if unprobed:
                crawler.sweep(typ, unprobed, absent, budget)

    # 4. finish any incomplete backfill
    for typ in args.types:
        if budget.exhausted:
            break
        if not (store.scan_state(typ).get("complete") or 0):
            _sweep_type(crawler, store, log, typ, budget, args.order)

    # 4b. full text + article pages for laws not yet covered (new laws)
    if not budget.exhausted:
        _fetch_lawtext(crawler, store, log, budget)
    if not budget.exhausted:
        _fetch_articles(crawler, store, log, budget)

    # 5. rolling revalidation of the oldest-checked documents
    if not budget.exhausted:
        rows = store.db.execute(
            "SELECT type, doc_id FROM doc WHERE present=1 ORDER BY last_checked "
            "LIMIT ?", (args.recheck,)).fetchall()
        bytype: dict[str, list[int]] = {}
        for typ, doc_id in rows:
            bytype.setdefault(typ, []).append(doc_id)
        for typ, ids in bytype.items():
            if budget.exhausted:
                break
            crawler.sweep(typ, ids, crawler.calibrate(typ), budget)

    c = crawler.counters
    log(f"[daily] fetched={c['fetched']:,} new={c['new']:,} changed={c['changed']:,} "
        f"same={c['same']:,} absent={c['absent']:,} errors={c['errors']:,}")
    store.finish_run(run, fetched=c["fetched"], new_docs=c["new"],
                     changed=c["changed"], errors=c["errors"], notes="daily delta")
    return crawler


def cmd_status(args):
    store = Store(DATA)
    db = store.db
    adv = {}
    for typ, total in db.execute(
            "SELECT type, SUM(expected) FROM (SELECT type, bucket,"
            " MAX(captured_at) ca, expected FROM advertised GROUP BY type, bucket)"
            " GROUP BY type").fetchall():
        adv[typ] = total
    print(f"archive: {DATA}")
    blobs = db.execute("SELECT COUNT(*), COALESCE(SUM(gz_bytes),0),"
                       " COALESCE(SUM(raw_bytes),0) FROM blob").fetchone()
    print(f"blobs  : {blobs[0]:,} files, {blobs[1]/1e9:.2f} GB on disk "
          f"({blobs[2]/1e9:.2f} GB raw)")
    pages = db.execute("SELECT COUNT(*) FROM page").fetchone()[0]
    print(f"index pages archived: {pages:,}")
    print()
    hdr = f"{'type':<12}{'held':>10}{'advertised':>12}{'probed':>10}{'max id':>10}  state"
    print(hdr); print("-" * len(hdr))
    for typ in DEFAULT_TYPES:
        row = db.execute(
            "SELECT COALESCE(SUM(present),0), COUNT(*), COALESCE(MAX(doc_id),0)"
            " FROM doc WHERE type=?", (typ,)).fetchone()
        st = store.scan_state(typ)
        exp = adv.get(typ) or 0
        if typ == "ruling":                     # two views over one corpus
            exp = max(exp, adv.get("ruling_adv") or 0)
        state = "complete" if (st.get("complete") or 0) else (
            f"cursor≤{st['ceiling']:,}" if st.get("ceiling") else "not started")
        print(f"{typ:<12}{row[0]:>10,}{exp:>12,}{row[1]:>10,}{row[2]:>10,}  {state}")
    print()
    ver = db.execute("SELECT COUNT(*) FROM version").fetchone()[0]
    chg = db.execute("SELECT COUNT(*) FROM doc WHERE n_versions>1").fetchone()[0]
    print(f"versions recorded: {ver:,} ({chg:,} documents seen changing)")
    for r in db.execute("SELECT run_id,mode,started,finished,fetched,new_docs,"
                        "changed,errors FROM run ORDER BY run_id DESC LIMIT 5"):
        print(f"  run {r[0]:<4} {r[1]:<9} {r[2]} → {r[3] or 'running':<21}"
              f" fetched={r[4]:,} new={r[5]:,} changed={r[6]:,} err={r[7]:,}")


def cmd_verify(args):
    """Fetch anything the site links to that the manifest does not hold."""
    store, log, fetch, crawler = build(args)
    store.db.execute("CREATE TABLE IF NOT EXISTS seed (type TEXT, doc_id INTEGER,"
                     " seen TEXT, PRIMARY KEY (type, doc_id))")
    budget = Budget(args.minutes * 60 if args.minutes else None, args.max_requests)
    missing = store.db.execute(
        "SELECT s.type, s.doc_id FROM seed s LEFT JOIN doc d"
        " ON d.type=s.type AND d.doc_id=s.doc_id WHERE d.doc_id IS NULL").fetchall()
    log(f"[verify] {len(missing):,} linked-but-unheld ids")
    bytype: dict[str, list[int]] = {}
    for typ, doc_id in missing:
        bytype.setdefault(typ, []).append(doc_id)
    for typ, ids in bytype.items():
        if typ in TARGETS and not budget.exhausted:
            crawler.sweep(typ, sorted(ids, reverse=True), crawler.calibrate(typ), budget)
    log(f"[verify] fetched={crawler.counters['fetched']:,} "
        f"new={crawler.counters['new']:,}")


def cmd_rehash(args):
    """Offline: re-derive presence and content hashes from the stored blobs.

    Run after changing `normalise` or `is_blank`, so the next recheck compares
    like with like instead of flagging the whole corpus as changed. Absent
    baselines are cleared and relearnt on the next network run.
    """
    import gzip
    store, log = Store(DATA), make_logger(DATA / "logs", args.quiet)
    db = store.db
    rows = db.execute(
        "SELECT d.type, d.doc_id, d.content_sha, b.path FROM doc d"
        " JOIN blob b ON b.blob_sha=d.blob_sha WHERE d.present=1").fetchall()
    read = lambda rel: gzip.open(store.blobs / rel).read()
    # blankness is a property of the page content: test one blob per old hash
    blank: dict[tuple[str, str], bool] = {}
    for typ, _, sha, rel in rows:
        if (typ, sha) not in blank:
            blank[(typ, sha)] = is_blank(read(rel))
    gone = rehashed = 0
    for i, (typ, doc_id, sha, rel) in enumerate(rows, 1):
        if blank[(typ, sha)]:
            db.execute("UPDATE doc SET present=0 WHERE type=? AND doc_id=?", (typ, doc_id))
            db.execute("DELETE FROM version WHERE type=? AND doc_id=?", (typ, doc_id))
            gone += 1
            continue
        new = content_hash(read(rel))
        if new != sha:
            db.execute("UPDATE doc SET content_sha=? WHERE type=? AND doc_id=?",
                       (new, typ, doc_id))
            db.execute("UPDATE version SET content_sha=? WHERE type=? AND doc_id=?"
                       " AND content_sha=?", (new, typ, doc_id, sha))
            rehashed += 1
        if i % 50_000 == 0:
            store.commit()
            log(f"[rehash] {i:,}/{len(rows):,}")
    db.execute("UPDATE scan SET absent_sha=NULL")
    store.commit()
    log(f"[rehash] {len(rows):,} held docs: {gone:,} were placeholders, "
        f"{rehashed:,} rehashed; absent baselines cleared")


def cmd_export(args):
    """Mirror every held document's raw page into the git-tracked archive/.

    archive/<type>/<id // 1000>/<id>.html holds the exact bytes the site served
    for the current version; archive/<type>.tsv indexes them. A file is only
    rewritten when the document's content changed (blob_sha moves only then),
    so git history is the change history. Placeholders are not exported.
    An index bigger than SPLIT_BYTES is written as parts instead,
    archive/<type>-<id // 100000>.tsv, each with the header, so no file
    reaches GitHub's 100 MB limit and a change only touches its id range.
    Read-only on the manifest, so it runs alongside a crawl without the lock.
    """
    import gzip
    out = Path(args.out) if args.out else ROOT.parent / "archive"
    db = sqlite3.connect(f"file:{DATA / 'manifest.sqlite'}?mode=ro", uri=True)
    log = make_logger(DATA / "logs", args.quiet)
    cols = ("doc_id", "url", "title", "first_seen", "last_changed",
            "content_sha", "blob_sha", "raw_bytes")
    types = [r[0] for r in db.execute("SELECT DISTINCT type FROM doc WHERE present=1")]
    SPLIT_BYTES, PART_IDS, MAX_BYTES = 50_000_000, 100_000, 95_000_000
    for typ in sorted(types):
        index = out / f"{typ}.tsv"
        parts = sorted(out.glob(f"{typ}-[0-9][0-9].tsv"))
        prev = {}
        for old in ([index] if index.exists() else []) + parts:
            for line in old.read_text(encoding="utf-8").splitlines()[1:]:
                f = line.split("\t")
                prev[int(f[0])] = f[6]
        rows = db.execute(
            "SELECT d.doc_id, d.url, d.title, d.first_seen, d.last_changed, d.content_sha,"
            " d.blob_sha, d.raw_bytes, b.path FROM doc d JOIN blob b ON b.blob_sha=d.blob_sha"
            " WHERE d.type=? AND d.present=1 ORDER BY d.doc_id", (typ,)).fetchall()
        wrote = 0
        for r in rows:
            if prev.pop(r[0], None) == r[6]:
                continue
            dest = out / typ / f"{r[0] // 1000:04d}" / f"{r[0]}.html"
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_bytes(gzip.open(DATA / "blobs" / r[8]).read())
            wrote += 1
        for gone in prev:  # no longer served; git history keeps the old page
            (out / typ / f"{gone // 1000:04d}" / f"{gone}.html").unlink(missing_ok=True)
        clean = lambda v: "" if v is None else str(v).replace("\t", " ").replace("\n", " ")
        index.parent.mkdir(parents=True, exist_ok=True)
        lines = ["\t".join(clean(v) for v in r[:8]) + "\n" for r in rows]
        head = "\t".join(cols) + "\n"
        if sum(len(l.encode()) for l in lines) < SPLIT_BYTES:
            files = {index: lines}
        else:
            files = {}
            for r, l in zip(rows, lines):
                files.setdefault(out / f"{typ}-{r[0] // PART_IDS:02d}.tsv", []).append(l)
        for old in ([index] if index.exists() else []) + parts:
            if old not in files:
                old.unlink()
        for path, ls in files.items():
            body = head + "".join(ls)
            if len(body.encode()) > MAX_BYTES:
                sys.exit(f"[export] {path.name} is {len(body.encode()):,} bytes; lower PART_IDS")
            path.write_text(body, encoding="utf-8")
        log(f"[export] {typ}: {len(rows):,} held, {wrote:,} written, {len(prev):,} removed")

    # index/listing pages (the site's own totals and year lists), named by url
    import re
    name = lambda url: re.sub(r"[^A-Za-z0-9.=-]+", "_", url.split("/", 3)[-1]) + ".html"
    index = out / "pages.tsv"
    prev = {}
    if index.exists():
        for line in index.read_text(encoding="utf-8").splitlines()[1:]:
            f = line.split("\t")
            prev[f[0]] = f[4]
    rows = db.execute(
        "SELECT p.url, p.first_seen, p.last_changed, p.content_sha, p.blob_sha, b.path"
        " FROM page p JOIN blob b ON b.blob_sha=p.blob_sha ORDER BY p.url").fetchall()
    for r in rows:
        if prev.get(r[0]) != r[4]:
            dest = out / "pages" / name(r[0])
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_bytes(gzip.open(DATA / "blobs" / r[5]).read())
    index.write_text("url\tfirst_seen\tlast_changed\tcontent_sha\tblob_sha\n" + "".join(
        "\t".join("" if v is None else str(v) for v in r[:5]) + "\n" for r in rows),
        encoding="utf-8")
    log(f"[export] pages: {len(rows):,} index pages")


def main(argv=None):
    p = argparse.ArgumentParser(prog="ul-scrape",
                                description="Raw archive of legallaw.ul.edu.lb")
    p.add_argument("--data", help="archive directory (default ./data)")
    p.add_argument("--workers", type=int, default=6,
                   help="concurrent requests; 6 is the measured sustainable "
                        "ceiling for this origin (12 collapses it)")
    p.add_argument("--delay", type=float, default=0.05,
                   help="minimum seconds between request starts")
    p.add_argument("--timeout", type=int, default=20,
                   help="a page that takes longer than this is a stalled "
                        "connection, not a slow page: the median response is "
                        "~0.3 s and the largest real documents ~4 s")
    p.add_argument("--retries", type=int, default=3)
    p.add_argument("--minutes", type=float, default=0,
                   help="wall-clock budget; 0 = unlimited")
    p.add_argument("--max-requests", type=int, default=None)
    p.add_argument("--stall-minutes", type=float, default=20.0,
                   help="abort if no request succeeds for this long, so a wedged "
                        "process cannot sit on the lock and block the 05:00 run")
    p.add_argument("--gap-tolerance", type=int, default=400,
                   help="consecutive absent ids treated as end-of-space")
    p.add_argument("--types", nargs="+", default=DEFAULT_TYPES, choices=DEFAULT_TYPES)
    p.add_argument("--order", choices=["asc", "desc"], default="desc",
                   help="sweep direction; desc fetches newest records first")
    p.add_argument("--tail", type=int, default=250,
                   help="ids to probe above each section's maximum (daily); a "
                        "day's new records are a handful, so this only needs to "
                        "cover the gap-riddled top of the space")
    p.add_argument("--recheck", type=int, default=3000,
                   help="least-recently-checked documents to revalidate per run")
    p.add_argument("--skip-indexes", action="store_true")
    p.add_argument("--out", help="export: target directory (default ../archive)")
    p.add_argument("--quiet", action="store_true")
    p.add_argument("cmd", choices=["indexes", "backfill", "daily", "status",
                                   "verify", "rehash", "articles", "export"])
    args = p.parse_args(argv)

    global DATA
    if args.data:
        DATA = Path(args.data)
    DATA.mkdir(parents=True, exist_ok=True)

    if args.cmd == "status":
        return cmd_status(args)
    if args.cmd == "export":
        return cmd_export(args)
    fn = {"indexes": cmd_indexes, "backfill": cmd_backfill, "daily": cmd_daily,
          "verify": cmd_verify, "rehash": cmd_rehash,
          "articles": cmd_articles}[args.cmd]
    with Lock(DATA / "ul-scrape.lock"):
        fn(args)


if __name__ == "__main__":
    main()
