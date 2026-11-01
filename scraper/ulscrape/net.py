"""Polite, dependency-free HTTP layer.

The target is a single elderly IIS 8.0 box serving a national legal corpus, so
the fetcher errs towards gentleness: low concurrency, a per-request delay, and
a circuit breaker that backs off hard when the server starts failing.
"""
from __future__ import annotations

import collections
import gzip
import hashlib
import random
import statistics
import re
import threading
import time
import urllib.error
import urllib.request
import ssl
import zlib


def _tls_context() -> ssl.SSLContext:
    """UL's chain ends at Sectigo Root R46, which python.org builds on macOS
    don't trust out of the box. Use certifi's bundle when it's installed."""
    try:
        import certifi
        return ssl.create_default_context(cafile=certifi.where())
    except ImportError:
        return ssl.create_default_context()


_TLS = _tls_context()

USER_AGENT = (
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 "
    "(KHTML, like Gecko) ul-law-archive/1.0 (+authorised archival crawl for "
    "Lebanese University, Centre for Legal Informatics)"
)

# ViewState/EventValidation are re-issued on every single response, so the raw
# bytes of an unchanged page differ on each fetch. Strip them (plus whitespace
# and the GA cache-buster) before hashing, or every nightly pass would look
# like the whole corpus changed.
_VOLATILE = re.compile(
    r'(id="(?:__VIEWSTATE|__EVENTVALIDATION|__VIEWSTATEGENERATOR|__PREVIOUSPAGE)"'
    r'[^>]*value=")[^"]*(")'
)
_WS = re.compile(r"\s+")
# WebForms echoes the requested query string back into the form's action, so two
# *different* non-existent ids yield pages that differ only there. Blanking it is
# what makes "is this id empty?" an exact hash comparison rather than a guess.
_FORM_ACTION = re.compile(r'(<form[^>]*\baction=")[^"]*(")', re.I)
# The per-document "view / PDF / print" toolbar embeds the record id in its
# hrefs, which varies the page for every id -- including ids with no record.
_TOOL_ANCHOR = re.compile(
    r'<a\b[^>]*\bid="MainContent_(?:viewLink|PrintDoc|downloadPDF|downloadWord'
    r'|downloadDoc|lnkPdf)"[^>]*>', re.I)
_HREF = re.compile(r'(\bhref=")[^"]*(")', re.I)
# The header prints today's date on every page. Left in, it gave the "no such
# id" page a new hash each day, so the absent baseline stopped matching and
# ~1.2M empty ids were archived as documents (fixed 2026-10-01).
_TODAY = re.compile(r'(<div id="HeaderControl1_tddatetoady"[^>]*>)[^<]*(</div>)')
# A missing record is served as HTTP 200 with a full page, in one of two shapes:
# legislation says "sorry, content not available"; rulings and research render
# an empty record card dated .NET's DateTime.MinValue.
_BLANK_MARKERS = ("المحتوى غير متوفر".encode(), b"01/01/0001")


def is_blank(raw: bytes) -> bool:
    """True when the page is the site's placeholder for a record that isn't there."""
    return any(m in raw for m in _BLANK_MARKERS)


def normalise(raw: bytes) -> str:
    """Return the stable, comparable form of a page.

    Removes the things that vary without the content changing: the
    per-response ViewState blobs, the self-referential form action, the
    document toolbar's id-bearing hrefs, and the header's date.
    """
    text = raw.decode("utf-8", "replace")
    text = _VOLATILE.sub(r"\1\2", text)
    text = _TODAY.sub(r"\1\2", text)
    text = _FORM_ACTION.sub(r"\1\2", text)
    text = _TOOL_ANCHOR.sub(lambda m: _HREF.sub(r"\1\2", m.group(0)), text)
    return _WS.sub(" ", text).strip()


def content_hash(raw: bytes) -> str:
    return hashlib.sha256(normalise(raw).encode("utf-8")).hexdigest()


TITLE = re.compile(r"<title>(.*?)</title>", re.S)


def page_title(raw: bytes) -> str:
    m = TITLE.search(raw.decode("utf-8", "replace"))
    if not m:
        return ""
    import html as _html
    return _WS.sub(" ", _html.unescape(m.group(1))).strip()


class Fetcher:
    """Serialises politeness across threads and trips a breaker on failure."""

    def __init__(self, base: str, fallback: str = "", delay: float = 0.25,
                 timeout: int = 90, retries: int = 4, log=None,
                 max_delay: float = 8.0, slow_ms: float = 15000.0):
        self.base = base if base.endswith("/") else base + "/"
        self.fallback = fallback
        self.delay = delay
        self.timeout = timeout
        self.retries = retries
        self.log = log
        self._lock = threading.Lock()
        self._next_at = 0.0
        self._consecutive_errors = 0
        self._use_fallback = False
        # Adaptive pacing. This origin has a small request pool: a dozen
        # sustained workers drove single-request latency from ~3s to >100s in a
        # couple of minutes. Rather than trust a fixed rate, watch latency and
        # widen the gap between requests whenever the server starts to labour.
        self.base_delay = delay
        self.max_delay = max_delay
        # Absolute rather than relative. A ratio test looks principled but
        # misfires badly here: an absent-id page is ~290 ms and a real document
        # ~1.8 s, so ordinary variation between the two reads as a 6x slowdown.
        # The steady state under the default 6 workers was measured at ~3 s, so
        # a sustained mean far above that is unambiguous overload.
        self.slow_ms = slow_ms
        # Latency history. "Usual" is the median of a rolling window rather than
        # a long exponential average: this origin answers in under a second when
        # idle and settles around 3 s under load, and an average seeded from the
        # idle figure spends the whole ramp-up mistaking normal for degradation.
        # A median over the window tracks the steady state instead.
        self._window = collections.deque(maxlen=200)
        # wall-clock of the last successful response, for the stall watchdog
        self.last_success_at = time.monotonic()
        self.stats = {"ok": 0, "err": 0, "bytes": 0, "retries": 0,
                      "throttled": 0}

    # -- pacing ---------------------------------------------------------
    def _wait_turn(self) -> None:
        """Reserve this request's slot on the shared schedule.

        `_next_at` is a cursor every worker pushes forward, which makes the two
        multipliers below dangerous: an error burst that multiplies the gap, with
        several workers each reserving a slot, can put the schedule minutes into
        the future and stall the crawl completely -- and because no requests then
        go out, nothing ever clears the error count that caused it. Both the
        per-request gap and how far the cursor may run ahead are therefore hard
        capped, so the crawl always recovers on its own.
        """
        with self._lock:
            now = time.monotonic()
            gap = self.delay * (1.0 + random.uniform(-0.25, 0.35))
            if self._consecutive_errors:
                gap *= min(2 ** self._consecutive_errors, 8)
            gap = min(gap, self.max_delay)
            start = max(now, min(self._next_at, now + self.max_delay))
            self._next_at = start + gap
        sleep = start - time.monotonic()
        if sleep > 0:
            time.sleep(sleep)

    def _note(self, ok: bool, elapsed_ms: float | None = None) -> None:
        with self._lock:
            if ok:
                self._consecutive_errors = 0
                self.stats["ok"] += 1
                self.last_success_at = time.monotonic()
            else:
                self._consecutive_errors = min(self._consecutive_errors + 1, 6)
                self.stats["err"] += 1
            if elapsed_ms and elapsed_ms > 0:
                self._window.append(elapsed_ms)
                self._adapt_locked()

    # ignore the first requests: startup noise must not trip the breaker
    MIN_SAMPLES = 60

    def _adapt_locked(self) -> None:
        """Widen or relax the inter-request gap when the origin is labouring."""
        if len(self._window) < self.MIN_SAMPLES:
            return
        recent = list(self._window)[-30:]
        # median, not mean: this origin stalls a small fraction of connections
        # for tens of seconds while serving everything else in ~300 ms, and a
        # mean would read those outliers as a service-wide collapse
        self._fast = statistics.median(recent)
        self._slow = statistics.median(self._window)
        if self._fast > self.slow_ms:
            new = min(self.delay * 1.5 + 0.05, self.max_delay)
            if new > self.delay:
                self.delay = new
                self.stats["throttled"] += 1
                if self.log and self.stats["throttled"] % 10 == 1:
                    self.log(f"throttling: median latency {self._fast:.0f}ms "
                             f"over {self.slow_ms:.0f}ms -> delay {self.delay:.2f}s")
        elif self._fast < self.slow_ms / 2 and self.delay > self.base_delay:
            self.delay = max(self.base_delay, self.delay * 0.9)

    # -- fetching -------------------------------------------------------
    def url_for(self, path: str) -> str:
        base = self.fallback if (self._use_fallback and self.fallback) else self.base
        return base + path.lstrip("/")

    def get(self, path: str) -> tuple[int, bytes, float]:
        """Return (http_status, body, elapsed_ms). Raises on final failure."""
        last = None
        for attempt in range(self.retries + 1):
            self._wait_turn()
            url = self.url_for(path)
            t0 = time.monotonic()
            try:
                req = urllib.request.Request(url, headers={
                    "User-Agent": USER_AGENT,
                    "Accept": "text/html,application/xhtml+xml,*/*;q=0.8",
                    "Accept-Encoding": "gzip, deflate",
                    "Accept-Language": "ar,en;q=0.8",
                    "Connection": "close",
                })
                with urllib.request.urlopen(req, timeout=self.timeout,
                                            context=_TLS) as resp:
                    body = resp.read()
                    enc = (resp.headers.get("Content-Encoding") or "").lower()
                    if "gzip" in enc:
                        body = gzip.decompress(body)
                    elif "deflate" in enc:
                        body = zlib.decompress(body, -zlib.MAX_WBITS)
                    status = resp.status
                # Every real answer from this site, a missing record included,
                # is a full page. An empty 200 is the origin failing: on
                # 2026-10-08 a run of them marked 3,000 laws withdrawn and
                # blanked every index page. Retry it like any other fault.
                if not body:
                    raise ValueError(f"empty {status} response")
                elapsed = (time.monotonic() - t0) * 1000
                self._note(True, elapsed)
                self.stats["bytes"] += len(body)
                return status, body, elapsed
            except urllib.error.HTTPError as e:
                # 404/410 are real answers about a record, not transport faults,
                # unless http.sys sent them: "Server: Microsoft-HTTPAPI" means no
                # website is bound to answer. On 2026-10-09 the origin 404'd every
                # URL that way, and 3,000 laws and 482 index pages went blank.
                server = (e.headers.get("Server") or "") if e.headers else ""
                if e.code in (404, 410) and "Microsoft-HTTPAPI" not in server:
                    self._note(True, (time.monotonic() - t0) * 1000)
                    return e.code, b"", (time.monotonic() - t0) * 1000
                last = e
                self._note(False)
            except Exception as e:                      # timeouts, resets, DNS
                last = e
                # a timeout is the strongest overload signal there is
                self._note(False)
                with self._lock:
                    # A timeout is a strong overload signal, but this origin
                    # stalls the odd connection even when healthy, so weight it
                    # lightly: a burst of timeouts still moves the median and
                    # trips the breaker, a lone stall does not.
                    for _ in range(3):
                        self._window.append(self.timeout * 1000.0)
                    self._adapt_locked()
                if isinstance(e, OSError) and "Name or service not known" in str(e):
                    self._use_fallback = True           # DNS gone: use the IP
                if "getaddrinfo" in str(e) or "resolve" in str(e).lower():
                    self._use_fallback = True
            if attempt < self.retries:
                self.stats["retries"] += 1
                backoff = min(2 ** attempt, 8) + random.uniform(0, 1.5)
                if self.log:
                    self.log(f"retry {attempt+1}/{self.retries} in {backoff:.1f}s "
                             f"({type(last).__name__}: {last}) {path}")
                time.sleep(backoff)
        raise RuntimeError(f"GET failed after {self.retries} retries: {path}: {last}")
