"""The scanning engine: calibrate, find the id ceiling, sweep, refresh."""
from __future__ import annotations

import concurrent.futures as cf
import threading
import time

from . import indexes
from .net import content_hash, is_blank, page_title
from .targets import ABSENT_PROBE_ID, STATIC_PAGES, TARGETS

# Hard stop for the ceiling search. Reaching it means the "does this record
# exist?" test is misfiring and every id looks alive -- fail loudly instead of
# galloping into an integer overflow.
MAX_PROBE_ID = 20_000_000


class Budget:
    """A wall-clock and request ceiling shared by every worker."""

    def __init__(self, seconds: float | None = None, requests: int | None = None):
        self.deadline = (time.monotonic() + seconds) if seconds else None
        self.max_requests = requests
        self.used = 0
        self._lock = threading.Lock()

    def take(self) -> bool:
        with self._lock:
            if self.deadline and time.monotonic() > self.deadline:
                return False
            if self.max_requests is not None and self.used >= self.max_requests:
                return False
            self.used += 1
            return True

    @property
    def exhausted(self) -> bool:
        if self.deadline and time.monotonic() > self.deadline:
            return True
        return self.max_requests is not None and self.used >= self.max_requests


class Crawler:
    def __init__(self, store, fetcher, workers: int = 6, log=print,
                 gap_tolerance: int = 400, stall_minutes: float = 20.0):
        self.store = store
        self.fetch = fetcher
        self.workers = workers
        self.log = log
        # how many consecutive absent ids to accept before declaring the end of
        # an id space (the site leaves real gaps where records were deleted)
        self.gap_tolerance = gap_tolerance
        # A crawl that hangs silently is worse than one that dies: this run is
        # measured in tens of hours, and the nightly job cannot take over while a
        # wedged process still holds the lock. Fail loudly instead.
        self.stall_seconds = stall_minutes * 60
        self.counters = {"fetched": 0, "new": 0, "changed": 0, "same": 0,
                         "absent": 0, "errors": 0}
        self._db_lock = threading.Lock()

    # -- primitives -----------------------------------------------------
    def _bump(self, key: str, n: int = 1) -> None:
        with self._db_lock:
            self.counters[key] = self.counters.get(key, 0) + n

    def calibrate(self, typ: str) -> str:
        """Fetch a deliberately invalid id to learn the 'record absent' page."""
        target = TARGETS[typ]
        if not target.baseline:
            # fetched only for ids we already know exist (and child urls need a
            # parent id), so there is no invalid-id page to learn: is_blank decides
            return ""
        st = self.store.scan_state(typ)
        if st.get("absent_sha"):
            return st["absent_sha"]
        path = target.path.format(id=ABSENT_PROBE_ID)
        _, raw, _ = self.fetch.get(path)
        sha = content_hash(raw)
        self.store.save_scan(typ, absent_sha=sha, min_id=target.start,
                             cursor=target.start)
        self.log(f"[{typ}] absent-page baseline {sha[:12]} ({len(raw)} bytes)")
        return sha

    def path_for(self, typ: str, doc_id: int) -> str:
        target = TARGETS[typ]
        if not target.parent:
            return target.path.format(id=doc_id)
        with self._db_lock:
            row = self.store.db.execute(
                "SELECT parent_id FROM link WHERE type=? AND doc_id=?",
                (typ, doc_id)).fetchone()
        if not row:
            raise KeyError(f"{typ}:{doc_id} has no recorded parent link")
        return target.path.format(id=doc_id, parent=row[0])

    def probe(self, typ: str, doc_id: int, absent_sha: str) -> str:
        """Fetch one id and persist the outcome. Returns the outcome string."""
        path = self.path_for(typ, doc_id)
        try:
            status, raw, _ = self.fetch.get(path)
        except Exception as e:
            self._bump("errors")
            self.log(f"[{typ}:{doc_id}] ERROR {e}")
            return "error"
        self._bump("fetched")
        if status in (404, 410) or not raw or is_blank(raw):
            with self._db_lock:
                self.store.record(typ, doc_id, self.fetch.url_for(path), False,
                                  None, None, status, "")
            self._bump("absent")
            return "absent"
        sha = content_hash(raw)
        present = sha != absent_sha
        with self._db_lock:
            outcome = self.store.record(
                typ, doc_id, self.fetch.url_for(path), present,
                raw if present else None, sha if present else None,
                status, page_title(raw) if present else "")
        self._bump({"new": "new", "changed": "changed",
                    "same": "same", "absent": "absent"}[outcome])
        return outcome

    def sweep(self, typ: str, ids, absent_sha: str, budget: Budget,
              chunk: int = 0) -> int:
        """Probe an iterable of ids in parallel, committing as it goes.

        Work is done in chunks so that a multi-day sweep checkpoints often: a
        machine that sleeps or a run that hits its budget loses at most one
        chunk of progress.
        """
        ids = list(ids)
        if not ids:
            return 0
        chunk = chunk or max(self.workers * 4, 32)
        done = 0
        with cf.ThreadPoolExecutor(self.workers) as pool:
            for offset in range(0, len(ids), chunk):
                if budget.exhausted:
                    break
                batch = [i for i in ids[offset:offset + chunk] if budget.take()]
                if not batch:
                    break
                list(pool.map(lambda d: self.probe(typ, d, absent_sha), batch))
                done += len(batch)
                self._check_stall()
                with self._db_lock:
                    self.store.commit()
                self.store.save_scan(typ, cursor=batch[-1])
                if done % (chunk * 10) == 0:
                    c = self.counters
                    self.log(f"[{typ}] {done:,}/{len(ids):,} probed "
                             f"(present={c['new'] + c['same'] + c['changed']:,} "
                             f"absent={c['absent']:,} err={c['errors']:,})")
        return done

    def _check_stall(self) -> None:
        idle = time.monotonic() - self.fetch.last_success_at
        if idle > self.stall_seconds:
            raise RuntimeError(
                f"no successful response for {idle/60:.1f} minutes -- aborting so "
                "the next scheduled run can take over rather than inheriting a "
                "wedged process holding the lock")

    # -- range discovery ------------------------------------------------
    def _live(self, typ: str, doc_id: int, absent_sha: str) -> bool:
        return self.probe(typ, doc_id, absent_sha) in ("new", "changed", "same")

    def _known_top(self, typ: str) -> int:
        """Highest id we have any reason to believe exists."""
        rows = self.store.db.execute(
            "SELECT MAX(doc_id) FROM doc WHERE type=? AND present=1", (typ,)).fetchone()
        top = rows[0] or 0
        try:
            seed = self.store.db.execute(
                "SELECT MAX(doc_id) FROM seed WHERE type=?", (typ,)).fetchone()[0] or 0
        except Exception:
            seed = 0
        return max(top, seed, TARGETS[typ].start)

    def extend_ceiling(self, typ: str, absent_sha: str, budget: Budget) -> int:
        """Walk upward from the highest known id in solid blocks.

        New records take the next id, so the top of the space is dense: probing
        every id in a block of `gap_tolerance` and stopping at the first wholly
        empty block finds the true ceiling exactly. Bisection cannot be used --
        these spaces contain gaps far wider than any sane probe interval, so a
        dead midpoint proves nothing.
        """
        step = self.gap_tolerance
        top = self._known_top(typ)
        ceiling = top
        sub = max(self.workers * 3, 12)
        with cf.ThreadPoolExecutor(self.workers) as pool:
            while not budget.exhausted:
                block = [i for i in range(ceiling + 1, ceiling + step + 1)
                         if budget.take()]
                if not block:
                    break
                hits: list[int] = []
                # commit in sub-chunks: some sections serve 500 kB pages and a
                # whole block can take many minutes, during which a crash would
                # otherwise lose every probe in it
                for off in range(0, len(block), sub):
                    part = block[off:off + sub]
                    hits += [d for d, ok in zip(part, pool.map(
                        lambda x: self._live(typ, x, absent_sha), part)) if ok]
                    with self._db_lock:
                        self.store.commit()
                if not hits:
                    break
                top = max(top, max(hits))
                ceiling += step
                self.log(f"[{typ}] ceiling probe: {len(hits)}/{len(block)} live "
                         f"up to {ceiling:,}")
                if ceiling > MAX_PROBE_ID:
                    raise RuntimeError(
                        f"[{typ}] records still live at id {MAX_PROBE_ID:,}: the "
                        "absent-page baseline is not matching, refusing to sweep")
        ceiling = top + step
        self.store.save_scan(typ, max_id=top, ceiling=ceiling)
        self.log(f"[{typ}] ceiling {ceiling:,} (highest live id {top:,})")
        return ceiling

    def survey_step(self, ceiling: int) -> int:
        """Sampling interval for the coarse survey.

        Scaled to the id space so that small spaces (Official Journal issues,
        ~17k ids) are sampled finely enough not to step over a short cluster,
        while large ones (legislation, ~334k ids) still cost only a couple of
        thousand probes.
        """
        return max(25, min(self.gap_tolerance, ceiling // 1500))

    def refine_gaps(self, typ: str, absent_sha: str, regions, ceiling: int,
                    budget: Budget) -> list[tuple[int, int]]:
        """Re-sample the dead space between known regions at a finer interval.

        The coarse survey can step over a cluster narrower than its interval;
        this pass shrinks that blind spot by a factor of four and folds anything
        it finds into the region list.
        """
        step = max(5, self.survey_step(ceiling) // 4)
        gaps: list[tuple[int, int]] = []
        prev = 0
        for lo, hi in regions:
            if lo - 1 > prev + 1:
                gaps.append((prev + 1, lo - 1))
            prev = max(prev, hi)
        if prev < ceiling:
            gaps.append((prev + 1, ceiling))
        points = [i for lo, hi in gaps for i in range(lo, hi + 1, step)]
        if not points:
            return regions
        self.log(f"[{typ}] refining {len(gaps)} dead gap(s) with "
                 f"{len(points):,} probes every {step}")
        found: list[int] = []
        with cf.ThreadPoolExecutor(self.workers) as pool:
            for off in range(0, len(points), self.workers * 4):
                if budget.exhausted:
                    break
                batch = [i for i in points[off:off + self.workers * 4] if budget.take()]
                if not batch:
                    break
                for doc_id, ok in zip(batch, pool.map(
                        lambda d: self._live(typ, d, absent_sha), batch)):
                    if ok:
                        found.append(doc_id)
                with self._db_lock:
                    self.store.commit()
        if not found:
            self.log(f"[{typ}] refinement found nothing new")
            return regions
        merged = [list(r) for r in regions]
        for point in found:
            merged.append([max(1, point - step * 2), min(ceiling, point + step * 2)])
        merged.sort()
        out: list[list[int]] = []
        for lo, hi in merged:
            if out and lo <= out[-1][1] + 1:
                out[-1][1] = max(out[-1][1], hi)
            else:
                out.append([lo, hi])
        result = [(a, b) for a, b in out]
        self.log(f"[{typ}] refinement found {len(found):,} new live point(s); "
                 f"{len(result)} region(s)")
        self.store.save_regions(typ, result)
        return result

    def survey_regions(self, typ: str, absent_sha: str, ceiling: int,
                       budget: Budget) -> list[tuple[int, int]]:
        """Coarsely sample the whole id space to find where records actually live.

        Large stretches of these id spaces were never used (laws, for instance,
        start around 1.7e5), and probing them one id at a time would cost tens of
        thousands of pointless requests. Sampling every `gap_tolerance`-th id
        costs a few hundred and bounds the dense sweep to the live regions.
        """
        step = self.survey_step(ceiling)
        points = list(range(1, ceiling + 1, step))
        self.log(f"[{typ}] surveying {len(points):,} sample points across "
                 f"[1..{ceiling:,}] every {step}")
        live: list[int] = []
        with cf.ThreadPoolExecutor(self.workers) as pool:
            for offset in range(0, len(points), self.workers * 4):
                if budget.exhausted:
                    break
                batch = [i for i in points[offset:offset + self.workers * 4]
                         if budget.take()]
                if not batch:
                    break
                for doc_id, ok in zip(batch, pool.map(
                        lambda d: self._live(typ, d, absent_sha), batch)):
                    if ok:
                        live.append(doc_id)
                with self._db_lock:
                    self.store.commit()
        if not live:
            return [(TARGETS[typ].start, ceiling)]
        regions: list[list[int]] = []
        for point in sorted(live):
            lo, hi = max(1, point - step), min(ceiling, point + step)
            if regions and lo <= regions[-1][1] + 1:
                regions[-1][1] = max(regions[-1][1], hi)
            else:
                regions.append([lo, hi])
        # always include the top of the space, where new records land
        top = self._known_top(typ)
        if regions[-1][1] < ceiling:
            if top - step <= regions[-1][1] + 1:
                regions[-1][1] = ceiling
            else:
                regions.append([max(1, top - step), ceiling])
        merged = [(a, b) for a, b in regions]
        span = sum(b - a + 1 for a, b in merged)
        self.log(f"[{typ}] {len(merged)} live region(s), {span:,} ids to sweep "
                 f"(vs {ceiling:,} for a blind scan): "
                 + ", ".join(f"{a:,}-{b:,}" for a, b in merged[:6])
                 + (" …" if len(merged) > 6 else ""))
        self.store.save_regions(typ, merged)
        return merged

    # -- index pages ----------------------------------------------------
    def refresh_indexes(self, budget: Budget) -> dict[str, int]:
        """Archive the static/index pages and record advertised totals."""
        totals: dict[str, int] = {}
        pages = list(STATIC_PAGES) + list(indexes.SEEDS.values())
        for path in pages:
            if not budget.take():
                break
            try:
                _, raw, _ = self.fetch.get(path)
            except Exception as e:
                self._bump("errors")
                self.log(f"[index] ERROR {path}: {e}")
                continue
            self._bump("fetched")
            with self._db_lock:
                self.store.record_page(self.fetch.url_for(path), raw,
                                       content_hash(raw))
            counts = indexes.year_counts(raw)
            if counts:
                typ = indexes.PAGE_TYPE.get(path) or next(
                    (t for t, u in indexes.SEEDS.items() if u == path),
                    path.split(".")[0])
                self.store.save_advertised(typ, counts)
                totals[typ] = sum(counts.values())
                self.log(f"[index] {typ}: {len(counts)} years, "
                         f"{sum(counts.values()):,} documents advertised")
        with self._db_lock:
            self.store.commit()
        return totals

    def harvest_year_pages(self, budget: Budget) -> dict[str, set[int]]:
        """Archive every per-year browse page and collect the ids it links to.

        Each of these views is truncated by the site, so they are not an
        enumeration -- but they are cheap, they pin documents to years, and
        anything they link to is something the archive must contain, which makes
        them a useful independent check on the id sweeps.
        """
        seeded: dict[str, set[int]] = {}
        paths: list[str] = []
        for typ in indexes.SEEDS:
            rows = self.store.db.execute(
                "SELECT DISTINCT bucket FROM advertised WHERE type=? "
                "ORDER BY bucket DESC", (typ,)).fetchall()
            paths.extend(indexes.year_browse_urls(typ, [r[0] for r in rows]))
        if not paths:
            return seeded
        self.log(f"[index] harvesting {len(paths):,} year-browse pages")

        def grab(path: str):
            try:
                _, raw, _ = self.fetch.get(path)
            except Exception as e:
                self._bump("errors")
                return path, None
            self._bump("fetched")
            return path, raw

        with cf.ThreadPoolExecutor(self.workers) as pool:
            for offset in range(0, len(paths), self.workers * 4):
                if budget.exhausted:
                    break
                batch = [p for p in paths[offset:offset + self.workers * 4]
                         if budget.take()]
                if not batch:
                    break
                for path, raw in pool.map(grab, batch):
                    if raw is None:
                        continue
                    with self._db_lock:
                        self.store.record_page(self.fetch.url_for(path), raw,
                                               content_hash(raw))
                    for t, ids in indexes.detail_ids(raw).items():
                        seeded.setdefault(t, set()).update(ids)
                with self._db_lock:
                    self.store.commit()
        return seeded
