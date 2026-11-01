# Static export of lebaneselegislation.com: spike

Status: a spike for review. Nothing is deployed, nothing is uploaded, no secret
is used, and no analytics code is touched. Branch
`grow/static-export-spike-20261101`, draft pull request.

The question: every page is data we already have, so why render it from
PostgreSQL on each request? Could the whole site be built from `data/` into
plain files, with one dynamic page for search?

## What the spike found

1. **`data/` rebuilds everything the pages read.** `data/load.sh` builds the
   database in 6 min 18 s (psql only). All 28 tables of `data/TABLES` are
   row for row identical to the production-like database (compared on every
   common column); the only difference is two internal provenance columns
   (`source_kind`, `source_id`) the export strips on purpose. No view or function
   is missing. One optional table is not in `data/`: `official_text` (see
   "What was missing").
2. **The exporter produces the live pages.** `cmd/static-export` calls the
   site's own handler (`site.New`) in process, so there is no second template
   implementation. With the live site's code on a database rebuilt from `data/`,
   2,000 random sitemap pages and 2,392 hub, list, topic, blog and sitemap pages
   (4,392) were compared with the live site: all identical apart from two things
   Cloudflare's edge adds on the way out (its analytics beacon and the
   obfuscation of e-mail addresses), except 3 pages that differ for reasons
   that are not missing data (below).
3. **The website code in this repository is not the live code yet.** Rendered
   from `data/`, 0 of 2,000 sampled pages equal the live page: the public copy
   of `internal/site` is an older generation (no Google Analytics script, no
   remembered language, no blog, other header and footer). A static build from
   this repository's `main` would change what visitors and crawlers get and
   would drop GA4. The cutover therefore waits for the website fold
   (`fold/4-website`), and the same proof below has to be run on it.
4. **Size and speed** (measured): about 14 GB raw and 3.4 GB gzipped for the
   whole site; 28 to 66 pages a second with 4 workers; under 200 MB of memory.
   A full render does not fit one standard GitHub-hosted job, so a full build
   has to be sharded (see "Later phase").

## What is in the branch

| Path | What |
|---|---|
| `internal/staticexport/` | the exporter (`export.go`), manifest, redirect rules for an edge (`redirects.go`), live comparison (`compare.go`), old-address check (`old.go`), the search page script (`searchshell.go`, `searchshell.js`), an R2 client and bounded uploader (`r2.go`), tests |
| `cmd/static-export/` | the command and its README |
| `workers/static-site/` | the Cloudflare Worker that would serve the files, with Node tests (later phase) |

The exporter reads the site's sitemaps through the handler, then follows the
internal links in the pages it renders, which reaches the pages the sitemaps
leave out (noindex pages, pager pages). It keeps one 64-bit hash per path and no
page bodies, so memory does not grow with the site. A manifest records each
path's status, SHA-256, size, content type and cache header; a rebuild reads it
and rewrites only the pages whose hash changed, and deletes the ones that went.

## Proof 1: `data/` is enough

```sh
createdb lebleg && data/load.sh "dbname=lebleg"        # 378 s on the XPS, loaded
```

Compared with the nightly database (`lebleg-nightly-pg`, the source of the
`data/` commit `bea882d`, 1 Nov 08:09), table by table on the columns both have:

| Result | Tables |
|---|---|
| identical rows (count and hash of every common column) | all 28: `legislation` 103,118, `legislation_article` 389,078, `ruling` 47,657, `ruling_page` 274,935, `graph_edge` 367,808, `noindex_page` 100,350, `source_page` 266,019, `page_state` 165,856, `ruling_file` 44,766, and the other 19 |
| columns only the nightly database has | `source_kind`, `source_id` on `legislation_article` and `article_version` (internal provenance, stripped by the export) |
| columns only `data/` has | none |

Pages, same code, two databases (nightly database against the `data/` rebuild):

| Pages | Identical | Different |
|---|---|---|
| 2,000 random pages from the live sitemaps (laws, decrees, texts, their articles, rulings, gazette issues, opinions, doctrine, agreements, English twins) | 2,000 | 0 |
| 2,392 pages of the small sitemaps (home, lists, years, pager pages, codes, topics, blog, `/changes/`) | 2,389 | 3 |

The 3 are not data:

- `/data/` and `/en/data/` print row counts read from `pg_class.reltuples`
  (docs.go, `reltuples::bigint`), a planner estimate that differs between two
  databases (388,615 against 389,590 for one table). Exact counts, or the
  numbers in `data/stats.json`, would make the page deterministic.
- `/decree/179090/` shows "Replaced or reworded" on one database and "Amended,
  as the Centre notes," on the other for one article. `attachLinks`
  (internal/site/links.go) reads `graph_edge` with no `ORDER BY` and keeps the
  first of two edges that share an amending article, so the physical row order
  decides. The edges are identical in both databases (ids included). A static
  build must be deterministic or a rebuild on another machine flips pages and
  re-uploads them: add a total order (relation, source) to that query.

## Proof 2: the exporter reproduces the live pages

Live site code (private repo, `main` at `0167867`; live runs `4e14fa6`, the same
for rendering) on the `data/` database, against the live site on 1 Nov 2026
(about 17:20 Beirut), fetched politely: 3 at a time, a pause between requests,
a User-Agent that names the check.

| Sample | Pages | Identical or edge-only | Other |
|---|---|---|---|
| random sitemap pages | 2,000 | 2,000 | 0 |
| small sitemaps and hubs | 2,392 | 2,385 | 7: `/data/`, `/en/data/`, `/decree/179090/` (above), and 4 search-form pages (below) |

What the edge adds, not counted as a difference:

- The Cloudflare Web Analytics beacon, inserted into HTML when the request
  accepts HTML (a request with `Accept: */*` does not get it). It is in the live
  page, not in the export. Whether it is still injected into HTML a Worker
  serves is **not documented** (the Web Analytics page names only
  `no-transform`, EU exclusion and the manual modes), so the first Worker test
  must check it; Cloudflare Web Analytics is the tracker-independent count the
  site relies on.
- E-mail obfuscation (`/cdn-cgi/l/email-protection`, plus its decode script).
  The export has the plain addresses; the same question applies to a Worker.

The 4 search-form pages (`/`, `/en/`, `/search/`, `/en/search/`) differ by one
paragraph, "Search includes answers from Google Gemini using our collection",
which the live process prints because its Gemini key is set. That is
configuration, not code or data: `static-export export -meaning-notice` renders
it (no request is ever sent to the provider) and with it all four match live.

## The public repository's website code against live, by cause

`internal/site` of `main` (`9470c50`) on the `data/` database, 2,000 pages
against the same live pages:

| Cause | Pages | What |
|---|---|---|
| private-only analytics: `ga.js` (Google Analytics 4: page, search, download, language events), `language.js` (remembered language), the `lang-switch` class the language event hooks on | 1,999 of 1,999 | live loads `/language.js`, `/ga.js`, `/assets/load.js`, `/main.js`, `/legislation.js`; this copy loads three of them |
| private-only blog: `blog.go`, `blog.json`, `blog_sitemap.go` | 1,999 | the blog feed link is missing from every page; `/blog/open-data-api-mcp/` is 404 here (live 200); the sitemap index has 21 shards, live 22 |
| layout generation: header lockup, footer headings (`h2` against `h4`), footer links, font preloads | 1,999 | |
| amendment disclosure (`<details class="art__amendments">`, "Amendments and earlier versions") | 54 of the pages that have amendments | |
| stale `data/` against live | 0 | `data/` is the 1 Nov nightly export, the same the live database was loaded from |
| views or functions missing from `data/schema.sql` | 0 | see below |

`internal/site` has 85 differences between the repositories (files that differ or
exist on one side only; 6 Go files exist only in the private one: `blog.go`, `events.go`, `hometopics.go`, `phcapture.go`,
`amounts.go`, `blog_sitemap.go`; three only here: `contribute.go`, `errors.go`,
`calculated.go`). The private-only changes the coordinator listed (analytics
#121 to #125, home topic links #127, server speed #130, hubs #131) are in that
set; none of them reaches this copy.

## What was missing

- `search_legislation` is the name of an MCP tool (a string in `mcp.go`), not a
  SQL function. No view or function is missing: the database from `data/` has
  `ar_norm`, `ar_light`, `ar_digits` and the views `article_cited`,
  `article_in_force`, `graph_node`, `legislation_summary`, `legislation_version`
  and `possibly_affected`, the same as the nightly database.
- `official_text` is in `db/official.sql`, not in `data/`, and not in the nightly
  database either. Without it `/official/` and `/en/official/` answer 500
  (`onpage.go` and `official.go` read it). **The live site answers 500 for both
  right now**, so the table is not in the live database today (a nightly swap
  drops it). After `psql -f db/official.sql` both answer 200. A restore from
  `data/` must run that file; the live bug is separate and worth fixing first.

## What the pages ask of the request

Everything a page shows is decided by its path. The inventory (a grep of
`internal/site` for the request, cookies and headers):

| Depends on | Where | In a static site |
|---|---|---|
| language | the `/en` prefix, then `withLang` (no cookie, no `Accept-Language`) | a path: `/en/...` is its own file. The saved choice (`language.js`) is `localStorage` plus `location.replace` in the browser |
| old addresses | `redirect.go`: `/ar/`, missing slash, wrong word, scanned-text alias, article spelling | `redirects.json` (247 KB, 13,742 decree and 13,307 text ranges, 49 aliases) and the rules in `redirects.go`, ported to the Worker and tested from one shared case table. Tested live: 383 of 383 old addresses answered with the live redirect. Article spellings other than Arabic-Indic digits ("12/1", "12-bis") stay with the origin |
| noindex | `noindex_page` table, and "any query string" | build time (it is a table); a query never reaches a static page |
| tracking parameters (`?utm_source=chatgpt.com`) | the largest referral source; today each is a Cloudflare cache MISS | ignored by the Worker's lookup, so these visits stay off the origin; the page's analytics read the URL in the browser |
| filters, sorting, `?page=` | list handlers (`handlers.go`, `docs.go`) | only the unfiltered list and its `/page/N/` paths are files; a filtered address goes to the origin |
| search | `GET /search/?q=`, `POST /search/`, `POST /search/answer/`, `/api/v1/search/` | one static `/search/` page (`-search-shell`) whose script calls `GET /api/v1/search/` and draws the results; the API stays on the origin. Same origin means no CORS; a different API host needs CORS on the Go API and its address in the CSP `connect-src`. The server-rendered search page also feeds `ga.js`'s `search` event through a hidden element; a static page has to send that event itself, which is analytics work this spike did not do |
| go to an article | `/law/N/go/?n=` | origin (a query) |
| notify form | `POST /api/notify`, answer `/ai/#notify-ok` | the Worker (`workers/static-site/src/notify.js`), stored in D1 (`migrations/0001_notify.sql`). Only the site's own form in a browser is accepted (Origin and Sec-Fetch headers), 5 tries an hour and 10 a day per address, 300 an hour overall, no way to read or delete through the Worker. The answer is a `#fragment`, so the page stays a static file. Until the D1 binding exists the origin answers as before |
| usage log | `events.go`: reads and searches written to a file, no visitor data | pages stop counting reads; `lebleg stats` loses them; search events stay with the API |
| rate limiting | `limiter.go` for the API and MCP | unchanged, origin |
| `/official/`, `/ruling-files/`, `/scan/`, `/ul/uploads/images/`, `/mcp`, `/healthz`, `/.well-known/`, `/ph/` | disk files, a database table, the MCP endpoint, the PostHog proxy | origin (Caddy proxies `/ph/` to PostHog; that is analytics and stays as it is) |
| response headers | Caddy: HSTS, CSP, frame, referrer and permissions policies; ShapBot 429 | the Worker adds the same set; a Node test compares its CSP with the Caddyfile |
| `Cache-Control` | `htmlCacheControl`: 12 hours at the edge, 5 minutes in browsers | stored with each object; a purge after each build, as after each nightly load today |

## Ruling PDFs

The ruling pages do not link `/ruling-files/<id>.pdf`. Of 150 rendered ruling
pages 0 contain that path (the code and the data page say "the link goes to the
scan at the source", `legallaw.ul.edu.lb/RulingFile.aspx?RuliID=N`, Nizar,
21 Oct); only `/data/` mentions it. The files are served on request:

- The origin gets them from `/srv/lebleg/ruling-files` on the Hetzner box
  (6.8 GB, 33,499 PDFs today), mounted read-only into the app container
  (`deploy/hetzner/compose.yml`), pushed from the XPS by
  `deploy/ruling-files-sync.sh` (rsync), from the folder `lebleg ruling-files`
  fills (`~/taktekhq/lebleg-ruling-files`, which also holds readings that are not
  served). `ruling_file` has 44,766 rows, so about 11,300 rows have no file on the
  origin yet.
- `rulingFile` answers `Cache-Control: public, max-age=86400` and
  `X-Robots-Tag: noindex`.
- The rendered pages do not need them, so the export does not either. The Worker
  sends `/ruling-files/` to the origin; moving them to R2 later is a separate,
  small job (about 7 GB, a few cents a month).

## Size, speed, memory

Measured on the XPS (12 cores, load average 8 to 22 from other work, database on
the same machine):

| Run | Result |
|---|---|
| 20,033 pages (random sample), 4 workers | 11 min 42 s, 28.6 pages/s, 495.8 MB raw, 108.3 MB gzip, peak heap 118 MB, 175 MB from the system |
| 2,000 pages, 4 workers, quieter machine | 30 to 54 s, 37 to 66 pages/s, 93 MB resident |
| 2,392 hub pages (large sitemaps) | 25 to 36 s |

Whole site, by the live sitemap counts (560,151 URLs) times the measured average
page by kind: 12.3 GB raw, 3.0 GB gzipped; the 100,350 noindex pages the sitemaps
leave out add about 1.6 GB raw, 0.4 GB gzipped. The 21 sitemap shards are 58 MB.
Averages: article 16 KB (4 KB gzip), law 24 to 44 KB, ruling 35 KB, decree 25 KB;
one code is 658 KB.

Time for a first full build at 28 to 50 pages a second: 4 to 8 hours of rendering
on one machine. Memory stays far below 1 GB.

## Later phase: where it runs

Not built in this branch (the uploader, the shard option and the Worker are code
without a workflow). Facts checked in Cloudflare's documentation on 1 Nov 2026:

| Fact | Value |
|---|---|
| Workers static assets, files per version | 20,000 Free, 100,000 Paid (25 MiB each) |
| Pages, files per site | 20,000 Free, 100,000 with the v4 build on paid plans |
| R2 objects per bucket | unlimited; one write a second to the same key |
| R2 Standard | $0.015 per GB-month, $4.50 per million writes, $0.36 per million reads, free tier 10 GB, 1 million writes, 10 million reads, free egress |
| Workers Paid | $5 a month, 10 million requests included then $0.30 per million |
| Hetzner volumes | 10 GB to 10 TB; the price is not on the documentation page (not verified) |

So 760,000 files do not fit Pages or Workers static assets. Two shapes fit:

1. **R2 and a Worker** (written, tested): files in a bucket, the Worker maps a
   path to a key, answers old addresses from `redirects.json`, adds the security
   headers, and sends anything it has no file for to the live origin. Cost for
   about 15 million page requests a month: about $7 to $12 (Workers requests
   dominate; storage is cents). Routes are attached one at a time and removing a
   route returns that path to the origin.
2. **Files on a bigger disk behind Caddy**: the 38 GB Hetzner disk has 5.7 GB free,
   so a volume is needed, and the origin stays a single point of failure (the 521
   clusters on 23 and 29 Oct).

Build and deploy belong in GitHub Actions, not on a VM or the XPS (its upload is
0.46 MB/s). One standard job cannot render the site: at 28 to 50 pages a second
760,000 pages take 4 to 8 hours and a job is limited to 6. The exporter has
`-shard i/n` for that; a full build is then about 8 parallel jobs of about 75
minutes each (restore 6 minutes, render about 95,000 pages), roughly 600 runner
minutes. That is free in a public repository and 20% of the private repository's
3,000 monthly minutes, so the workflow belongs in the public repository once the
website is there. Nightly runs should render only what changed: derive the
changed laws from the `data/` diff between two commits and re-render those pages
plus the hubs. Two pieces are still to write: that change list, and the step that
merges the shards' manifests and deletes what is gone.

`data/` alone is enough to restore the database in a job (Proof 1), plus
`db/official.sql`. A dry run on one machine with `act` needs the workflow first;
until then the commands in `cmd/static-export/README.md` are the dry run.

## Decisions and risks

- **Land the website fold first.** The proof above must be repeated on the
  folded site (`static-export export -sample 2000`, then `compare`); the target is
  0 differences beyond the edge rewrites.
- **Make `attachLinks` deterministic** and the `/data/` counts exact before a
  static build, or rebuilds flip pages.
- **Fix `/official/`** (500 on live now): run `db/official.sql` after every
  restore, in the nightly too.
- **Edge features on a Worker are unverified**: the Web Analytics beacon and
  e-mail obfuscation. Test on a preview hostname; if the beacon is not injected it
  can be added at export time (its token is in the live page source), which is
  analytics work for its owner.
- **GA4 search event** for the static search page needs an owner's decision.
- **Per-page reads** stop being counted by the server (`events.go`).
- A static site is as stale as its last build. Today the edge holds a page up to
  12 hours and is purged after the nightly load, so the staleness is the same.
