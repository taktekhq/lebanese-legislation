# UL Legal Informatics: raw archive

A complete, resumable raw archive of the Lebanese University **Centre for
Research and Studies in Legal Informatics**
(مركز الأبحاث والدراسات في المعلوماتية القانونية), `legallaw.ul.edu.lb`
(same host as `77.42.251.205`).

Everything the site serves is stored **verbatim**. Nothing is parsed,
rewritten, or "cleaned". That is deliberate, so the eventual search / LLM
layer can be rebuilt from scratch at any time without re-crawling a slow
origin server.

```bash
bin/ul-scrape indexes     # archive index pages + record the site's own totals
bin/ul-scrape backfill    # sweep the id space of every section (resumable)
bin/ul-scrape daily       # nightly delta (this is what the 05:00 job runs)
bin/ul-scrape status      # progress vs. the site's advertised totals
bin/ul-scrape verify      # fetch anything the site links to that we lack
bin/ul-scrape rehash      # offline: recompute presence + hashes from blobs
bin/ul-scrape articles    # full text of every law, then each article page
bin/ul-scrape export      # mirror held pages into ../archive (the archive submodule)
bin/publish               # export, then commit + push per type to the archive repository
```

## What is on the site

| Section | Arabic | Documents (site's own count) | Detail URL |
|---|---|---|---|
| Legislation | النصوص القانونية | **101,570** (1900–2025) | `Law.aspx?lawId=N` |
| Decisions | القرارات | **47,350** (1900–2019) | `ViewRulePage.aspx?ID=N` |
| Judgments | الأحكام | **42,648** (1900–2024) | `ViewRulePage.aspx?ID=N` |
| Advisory opinions | الآراء الاستشارية | **3,631** | `ViewOpinionPage.aspx?ID=N` |
| Agreements | الاتفاقيات | **1,733** (1921–2024) | `ViewAgreementPage.aspx?ID=N` |
| Bibliography / articles | القاعدة البيبليوغرافية | **848** (2007–2024) | `RefPageDetails.aspx?id=N` |
| Research & studies | الأبحاث والدراسات | not shown | `ViewResearchPage.aspx?id=N` |
| Official Journal | الجريدة الرسمية | issue pages | `OfficialJournal.aspx?Journal=N` |
| News / static pages | الأخبار / صفحات | not shown | `NewsDetailPage.aspx?id=N`, `CustomPage.aspx?id=N` |

Judgments and Decisions are two views over one ruling corpus and share the
`ViewRulePage.aspx` id space, so the archive holds them once, under `ruling`.

**Not covered by this scraper:** parliamentary minutes
(محاضر مجلس النواب) live in a separate static subsite at
`legallaw.ul.edu.lb/parliament/` in `windows-1256`, and are better mirrored
with `wget -m` than crawled by id.

## Why it enumerates ids instead of crawling the search pages

This was the central design question, and the site forces the answer:

* **Every list and search view is silently truncated.** Legislation search
  returns at most 20 rows for a query the site itself labels `2012 (3581)`;
  agreements return 10. There is no results pager anywhere, the only pager in
  the markup (`ShowNext()`/`ShowLast()`) scrolls the *year selector*, entirely
  client-side.
* **There is no sitemap and no robots.txt** (both return HTTP 500).
* **Detail pages are directly addressable by a single numeric id**, and ids are
  assigned in insertion order, so new documents always appear at the top of the
  space.

So the id space is the only enumerable surface, and the site's own per-year
counts are the yardstick that says whether the sweep found everything , 
`ul-scrape status` prints held vs. advertised side by side.

## How "does this record exist?" is decided

Asking for a non-existent id returns **HTTP 200** with a page that merely has
empty content regions, so status codes are useless. Instead each section is
calibrated once against a deliberately invalid id, and a page counts as a real
record when it differs from that baseline.

The placeholder is recognised first by its content (`net.is_blank`):
legislation says «عذرا المحتوى غير متوفر حاليا», rulings, research and
agreements render an empty card dated `01/01/0001`. The hash baseline is the
second line of defence. Relying on the hash alone failed once: the header
prints today's date, so the baseline drifted daily and ~1.3M empty ids were
archived as documents until 2026-10-01 (`rehash` reclassified them).

For the comparison to be exact, three things that change without the content
changing are normalised away (`net.normalise`):

1. `__VIEWSTATE` / `__EVENTVALIDATION`: **re-issued on every response**, so the
   raw bytes of an unchanged page differ on every fetch;
2. the `<form action>`, which echoes the requested query string back;
3. the per-document toolbar hrefs (`view` / `PDF` / `print`), which embed the id.

Without (2) and (3) two *different* non-existent ids hash differently and the
existence test collapses, this was verified across all nine sections, with
four different invalid ids each.

This normalisation is also what keeps storage finite: because ViewState makes
every raw response unique, hashing raw bytes would store a fresh copy of the
entire corpus on every nightly run.

## Storage layout

```
data/
├── blobs/ab/cd/<sha256>.html.gz   raw HTML exactly as served, gzip -9
├── manifest.sqlite                provenance, history, scan state
└── logs/YYYY-MM-DD.log
```

`manifest.sqlite`:

| table | purpose |
|---|---|
| `doc` | one row per `(type, id)` ever probed, present or not, current hash, title, first seen, last checked, last changed |
| `version` | every distinct content version ever observed, so edits to a law are auditable |
| `blob` | content-addressed payloads; an unchanged document is stored once no matter how often it is re-checked |
| `page` | index/search pages, archived verbatim on every run |
| `advertised` | the site's own per-year counts, the completeness yardstick |
| `region`, `scan` | where ids actually live, and how far the sweep has got |
| `seed` | ids the site links to, used by `verify` |
| `run` | one row per invocation |

Read a document back out:

```python
from ulscrape.store import Store
s = Store("data")
sha = s.db.execute("SELECT blob_sha FROM doc WHERE type='law' AND doc_id=333659").fetchone()[0]
html = s.read_blob(sha)           # exactly the bytes the server sent
```

## Sweep strategy

1. **`extend_ceiling`**: walk upward from the highest id known to exist in solid
   blocks of `--gap-tolerance` (400) ids, stopping at the first wholly empty
   block. The top of the space is dense, so this pins the ceiling exactly.
   Bisection is *not* used: these id spaces contain gaps far wider than any
   sane probe interval, so a dead midpoint proves nothing.
2. **`survey_regions`**: sample every 400th id across the whole space to find
   where records live. Legislation ids, for instance, start near 170,000, and
   sampling turns ~334,000 blind probes into a few hundred.
3. **Dense sweep** of the live regions, newest id first (`--order desc`), so the
   most-wanted material lands first. Progress is checkpointed every chunk, so
   the run is interruptible and resumable.

## Daily refresh

Run `bin/ul-scrape daily` once a day and `bin/publish` after it, from any
scheduler (we use systemd user timers: `daily` at 05:00 with a 3-hour budget,
`articles` every 30 minutes, `publish` every 6 hours). Set `UL_DATA` to keep the
crawler's own store (manifest, blobs, logs) outside the checkout.

`bin/publish` mirrors the archive into a checkout of
[taktekhq/lebanese-legislation-archive](https://github.com/taktekhq/lebanese-legislation-archive)
(`LEBLEG_ARCHIVE_REPO`, else this repository's `archive/` submodule) and pushes
one commit per type to its `main`. `bin/check-archive` checks that a published
commit is less than a day old and that no file is over GitHub's 100 MB limit.

`ul-scrape daily` (3-hour budget) does, in order: re-archive the index and
year-browse pages; fetch anything newly linked; push each section's ceiling
upward to catch brand-new records; continue the backfill if it is unfinished;
then spend whatever budget is left re-checking the least recently verified
documents, so the whole corpus is revalidated on a rolling basis and edits are
captured in `version`.

A lock file prevents a 05:00 run from overlapping a still-running backfill.

## Politeness

The origin is one elderly IIS 8.0 box serving a national legal corpus to real
users, and it has a small request pool. This was measured, not guessed:

| sustained load | throughput | latency | verdict |
|---|---|---|---|
| 4 workers, real documents | 0.97 req/s | 4.2 s, flat | fine |
| **6 workers, real documents** | **~2 req/s** | **3.1 s, flat, 0 failures** | **default** |
| **6 workers, empty-id probes** | **7.6 probes/s** | median 333 ms, p90 484 ms | 0 retries, 0 throttling |
| 10 workers | 1.2 probes/s | median 481 ms, stalls to 45 s | worse than 6, stalls dominate |
| 12 workers | not measured | 3 s → **>100 s** within two minutes | collapses the server |

Note the two regimes: a real document is ~40 kB and ~3 s, an empty id ~23 kB and
~0.33 s, so a sweep's rate depends entirely on how many live records it is
crossing. Ten workers is *slower* than six, not just riskier.

Two things here are easy to get wrong. A short burst at 16–24 workers looks
fine, which is misleading, the damage only shows under sustained load: at 12
workers single-request latency degraded from ~3 s to over 100 s, every request
for one section began timing out, and the site recovered within 45 s of
stopping. And the origin answers in well under a second when idle, so any
"this page is slow" conclusion drawn mid-crawl is really a measurement of our
own load. **Do not raise `--workers` above 6 without re-measuring.**

### Stalled connections are not a slow server

A small fraction of connections to this origin hang for tens of seconds while
everything else is served in ~300 ms (median 335 ms, p90 well under a second,
occasional outliers past 30 s). Two consequences are baked into the defaults:

* **`--timeout` is 20 s, not 60.** The largest real documents arrive in ~4 s, so
  anything past 20 s is a stalled connection. A 60 s timeout with several retries
  let a single stall occupy a worker for minutes, and with six workers that alone
  dropped throughput to near zero.
* **The throttle compares medians, not means.** Two 30 s outliers in a 30-sample
  window drag a mean over any sane threshold, so a mean-based breaker reads
  normal service as a collapse and throttles for no reason.

### The pacing cursor is bounded on purpose

Workers reserve their slots on a shared schedule cursor. Multiplying the gap on
errors *and* letting every worker push that cursor forward is a deadlock: an
error burst can put the schedule minutes into the future, no requests go out, and
because no requests go out nothing ever clears the error count that caused it.
The crawl stops dead without a single exception in the log. Both the per-request
gap and how far the cursor may run ahead of now are therefore hard-capped at
`max_delay`, so the crawler always recovers unaided. If you change
`net.Fetcher._wait_turn`, preserve that.

Because ~155,000 documents must each be fetched once (plus a few tens of
thousands of probes for empty ids), a full backfill is ~325,000 requests, or
roughly 45 hours at the sustainable rate, a multi-day job. It is designed to be left running and to be resumed by the
nightly job if interrupted.

## Worth knowing for the search / LLM layer

The site also exposes cleaner per-document renderings, not currently archived
(each would roughly double the request count):

* print/plain views, `RulingView.aspx?opt&RulID=N&TYPE=PRINT`,
  `researchesView.aspx?opt&RulID=N&TYPE=PRINT`,
  `AgreementView.aspx?ID=N&TYPE=PRINT`, `OpinionView.aspx?opt&ID=N`
* PDF endpoints, `DownloadAgreementPage.aspx?Target=All&type=2&ID=N`,
  `DownloadOpinionPage.aspx?...`

Legislation also carries structured metadata worth extracting later: document
type (45 values: قانون, مرسوم, قرار…), status (قيد التطبيق / ملغى), a
classification tree, article numbers, and the Official Journal issue each text
was published in, which is exactly the axis set for the "old normal filtering"
alongside the LLM.

## Law text

`Law.aspx` carries a law's metadata and table of contents, not its text. The
text comes from two further page types, both fetched by `articles`:

- `lawtext`: `LawView.aspx?opt=view&LawID=N`, the whole law on one page:
  Arabic, the French text where one exists, chapter headings, signatures. One
  request per law. The longest codes take ~40 s, hence `--timeout 180`.
- `lawarticle`: `LawArticles.aspx?LawArticleID=A&LawId=N`, one article with
  its own effective date (تاريخ بدء العمل). The url needs the law id, so ids come
  from links on law pages (`links.py`, table `link`). About 2% of laws list
  only chapters; their chapter pages (`lawsection`) are fetched until every
  article is known.
