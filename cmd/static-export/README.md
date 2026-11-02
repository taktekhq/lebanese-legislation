# static-export

Writes the Lebanese Legislation website as plain files and checks the result
against a live site. It does not render anything itself: it calls the site's own
handler (`internal/site`) in process, against a PostgreSQL database, so an
exported page is the page that handler serves. The deployed setup is described in [docs/static-export.md](../../docs/static-export.md).

```sh
# 1. a database, from this repository's own data/ (about 6 minutes, psql only)
createdb lebleg && data/load.sh "dbname=lebleg"
psql lebleg -f db/official.sql            # the one table data/ does not carry (/official/)

# 2. the whole site, 4 pages at a time, into ./site
export LEBLEG_DSN="dbname=lebleg"
go run ./cmd/static-export export -out site -workers 4 -base https://example.org

# 3. only some pages
go run ./cmd/static-export export -out site -paths paths.txt
go run ./cmd/static-export export -out site -sample 2000 -seed 1
```

A full export writes `<path>/index.html` for every page, other responses
(sitemaps, `robots.txt`, feeds) under their own name, `404.html`, the
stylesheets and scripts, and `.export/`:

| File | What |
|---|---|
| `manifest.jsonl` | one line per path: status, SHA-256, size, content type, file, `Cache-Control`, redirect target |
| `redirects.json` | what an edge needs to answer old addresses in one hop (see below) |
| `removed.txt` | pages the previous export had and this one does not |
| `stats.json` | pages, bytes (raw and gzip), time, peak memory, size by kind of page |
| `server.log` | what the site logged while rendering (slow pages, errors) |

A second run reads the previous manifest and rewrites only the pages whose hash
changed; pages that stopped existing are deleted. Pages are found from the
site's own sitemaps, then by following the internal links in the pages it
renders (`-crawl`, on by default), which reaches the pages the sitemaps leave
out. Query strings are never followed.

## Checking an export against a live site

```sh
go run ./cmd/static-export sample  -live https://example.org -n 2000 > paths.txt
go run ./cmd/static-export export  -out site -paths paths.txt
go run ./cmd/static-export compare -out site -live https://example.org -paths paths.txt -save-live live/
go run ./cmd/static-export compare -out site -from-dir live/ -paths paths.txt   # again, offline
```

`compare` fetches each page politely (3 at a time, a pause between requests, a
User-Agent that says what it is), normalises whitespace and the build id in
asset links, and reports each page as identical or by what differs (title,
canonical, robots, hreflang, structured data, visible text, markup) and groups
the differences by what differs. Two things Cloudflare adds at its edge are
named instead of counted as differences: the Web Analytics beacon and the
obfuscation of e-mail addresses.

Old addresses (under `/ar/`, without the final slash, with the wrong word for
a text, Arabic-Indic digits in an article number) are checked with:

```sh
go run ./cmd/static-export export    -out site            # a full export writes redirects.json
go run ./cmd/static-export old-urls  -out site -n 400 > old.txt
go run ./cmd/static-export check-old -out site -live https://example.org -paths old.txt
```

## Upload and shards

`-r2-bucket` uploads changed pages to a Cloudflare R2 bucket instead of keeping
them on disk (64 to 128 uploads in flight, retries, nothing written to the
runner's disk), `-dry-run` counts what would be sent, `-shard i/n` renders one
slice of the site so the work can be split across jobs. The keys are
`R2_ACCESS_KEY_ID` and `R2_SECRET_ACCESS_KEY` in the environment, never flags.
The [static-site workflow](../../.github/workflows/static-site.yml) runs 24 parallel shards to render about 760,000 pages from `data/` into R2 bucket `lebleg-site`. The JavaScript Worker in `workers/static-site` serves the files and ruling PDFs; dynamic requests go to one Cloudflare Container. See [architecture](../../docs/architecture.md).

Crawling shards exchange discovered paths in R2 after each round of owned
pages. Every shard must run together with the same database, shard count and
fresh `-exchange-id`; GitHub Actions supplies the run id and attempt automatically.
The runner concurrency allowance must accommodate all shards together; after a
failed attempt, rerun all render jobs with a fresh id, including previously
successful shards. A shard only renders and records paths assigned to it by `Owner`, including
extra paths. The exchange continues until every shard has no new foreign links,
so paths reached through other owners, redirects and cycles are included once.
`-exchange-timeout` (default `30m`) bounds each exchange wait, not rendering.
If it expires, the error lists the shards that have not reported for that round.
Coordination objects live under `_export/discovery/<id>/of-<n>/`.
Local independent shards can use `-crawl=false` for sitemap-only exports;
coordinated crawling requires R2 and cannot use `-dry-run` or `-limit`.
At about 760,000 pages, 24 shards average about 31,667 pages each.

## Tests

```sh
go test ./internal/staticexport ./cmd/static-export
cd workers/static-site && node --test test/*.test.mjs
```

The exporter's tests use a fake site, so they need no database. The signer is
checked against the example in the AWS Signature Version 4 documentation.
