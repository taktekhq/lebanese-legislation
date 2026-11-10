# How the build makes reads fast

The hosted server answers most MCP tool calls in a few milliseconds on one CPU.
It does so because the expensive work happens once, when the container image is
built, not on every request. This page says what is built, why it is safe, how
it was measured and how to add to it.

Short version, measured on real calls to `https://lebaneselegislation.com/mcp`
(server time, 9 Nov 2026):

| Tool | Before | After |
| --- | --- | --- |
| `get_article`, real calls, median / p95 / slowest | 128 / 1,060 / 3,242 ms (7 days before) | 13 / 131 / 199 ms (25 calls, first 12 hours) |
| `get_law`, 6 calls at once | 160 to 382 ms | 31 to 104 ms |
| `search_legislation`, 6 at once, warm | 19 to 178 ms | 17 to 90 ms |
| `list_legislation`, 6 wide calls at once | 243 to 290 ms | 9 to 16 ms |

Every reply is byte-identical to the reply before the change.

## Why it works

The served database is read-only. `scripts/nightly.sh` rebuilds `data/` from
the source pages, the data workflow bakes it into the `-db` image, and nothing
writes to it while it serves. So any result that is the same for every caller
can be computed once per build.

The origin is one container with one CPU, shared by PostgreSQL and the Go
server. Work done per request is paid again by every caller, and a burst pays
it all at once on that one CPU. On 8 Nov 2026 an assistant sent 10
`get_article` calls within about 60 ms: six took 3.2 to 3.4 s and four got a 503.

We did not add a cache or a warm-up. A cache has to be filled after every
deploy, holds memory on a 6 GB container, and can go stale. Build-time tables
are just data: PostgreSQL reads them from disk like any other table.

## What the build makes

Three layers, each built by the workflow that owns its input.

| Layer | Built by | What |
| --- | --- | --- |
| Database build (`db/`, in `-db`) | `data.yml` job `db` | `search_term`, a materialized view with how many texts hold each word, each search index's size and average text length. Stored columns `text_len`, `search_fr`, `title_exact`, `title_base`, `search_en`. 13 list indexes that answer cursor pages. |
| Read-optimized tables ([`db/prod.sql`](../db/prod.sql), in `-db-prod`) | `data.yml` job `db-prod` | The six tables below. |
| Search word lists (in `-all`) | a stage of the [container Dockerfile](../workers/static-site/container/Dockerfile) | One file, `/opt/lebleg/search-index`: for each word, the texts that hold it. |

`db/prod.sql` runs once on the baked database:

| Table | Replaces, per request | Size and build time |
| --- | --- | --- |
| `possibly_affected_t` | A view that joined the link graph to itself: 214 of `get_article`'s 232 ms | 808 kB |
| `law_card` | Joins for the law summary every reply carries | 49 MB |
| `article_card` | Resolving each article's wording in force | 340 MB |
| `law_link` | Reading every link into a law to list its amending texts (22,861 edges read for the Penal Code, 538 kept) | 37,672 rows, 19 MB, 2.5 s |
| `search_known` | A join to all 103,112 laws for every search candidate | one row |
| `law_pub_count` | `count(*)` over every text for a list's total: 26 to 40 ms | 23,975 rows, 2.5 MB, 0.1 s |

`law_pub_count` holds running counts: for each kind and publication day, how
many texts were published up to that day. A total for a period is one stored
number minus another, two index reads.

The compressed `-db-prod` image is 2.434 GB against 2.149 GB for `-db`. The
tables hold no new data. `-db`, the public dataset image, and the Hugging Face
Parquet files do not carry them.

## The search word lists

`lebleg-web search-index FILE` reads the words PostgreSQL already extracted
for its search indexes and writes one list per word: the numbers of the texts
that hold it, as varint gaps, plus each text's ranking inputs (length, date,
year, number). 87.5 MB, 52 MB gzipped, 15 s on one CPU.

The server maps the file read-only (`mmap`). It copies 12 MB of per-text
columns and reads the lists in place, so the kernel pages them in and can drop
them, as it does for PostgreSQL's files. It is ready 70 to 110 ms after start
and uses SQL until then. PostgreSQL still plans the words (normalizing,
stemming, prefixes) and fetches the rows of the results.

We also built the same index as a PostgreSQL table to compare:

| | SQL only | Lists in a table | Lists in the mapped file |
| --- | --- | --- | --- |
| CPU per search | 13 to 15 ms | 7 to 9 ms | 4 to 6 ms |
| p95, 6 searches at once, one pinned core | 134 to 144 ms | 71 to 74 ms | 54 to 56 ms |
| Size | | 432 MB in the database | 87.5 MB file |
| Build time | | 133 s | 15 s |

The database can hold the index. Reading it in the server's own process wins,
because each list read from a table costs a round trip and a copy.

## Why it cannot break a reply

1. **Version marks.** Each table carries a comment such as `lebleg-prod 4`.
   The server checks the marks once. A missing or older table means it runs the
   SQL it ran before. The word lists carry a fingerprint of the database (text
   counts, average lengths, highest ids); on a mismatch the server searches
   with SQL. So `-mcp` and Compose still work on plain `-db`, and an image
   built from another `prod.sql` cannot return an error.
2. **Byte-identical replies.** A change merges only if a fixed set of 622 calls
   (322 across five tools, 100 more `get_law`, 200 real search questions) gives
   the same SHA-256 on the old and new server, and again with the mark switched
   off. For search, 21,169 answers were compared field by field with scores to
   the bit, and none differed.
3. **Undetermined cases go back to SQL.** Where the old query's own answer
   varies from run to run (texts of one length on both sides of a cut-off), the
   index does not guess. That is 4.9% of search answers.

## How it was measured

- The live image, in Docker with the live limits: `--cpus=1 --memory=6g`,
  `shared_buffers` 1 GB, `jit=off`, the real `internal/mcpserver` in the same
  container.
- Calls alone, 6 at once and 10 at once. p95 is the time 95 of 100 calls finish
  within.
- Both CPU modes. `--cpus=1` on a many-core machine is a quota: a burst runs on
  several cores, uses the quota up and stalls for the rest of each 100 ms
  period. The same burst pinned to one core (`--cpuset-cpus`) is 2 to 3 times
  faster. We report both.
- Then live: bursts with a User-Agent the site counts as a monitor, read as
  server time in analytics, and real calls once they arrive. Our own checks are
  left out of the response-time charts.

## What is still slow

- The first minute after a deploy. A new container has nothing in the page
  cache: six searches one second after a switch took 104 to 298 ms.
- Large replies. `get_law` for a big code returns up to 284 KB; the transfer
  takes longer than the server.
- The website's search page and `/api/v1/search/` use the same engine without
  the word lists, because their searches page with cursors.

## Adding a build-time table

1. Profile first: `EXPLAIN ANALYZE` the logged SQL at the live limits. Move only
   what is the same for every caller.
2. Add the table to `db/prod.sql` with a new mark, grant it to `lebleg_web` and
   `lebleg_reader`, and `ANALYZE` it.
3. In `internal/mcpserver`, read it only when the mark matches, and keep the
   old SQL as the fallback. Bump the mark in `prod.sql` and in the server
   together when a column changes.
4. Test that both paths agree (`TestLayoutsAgree`, `TestOldShape`) and that the
   reply dump is byte-identical.
5. Merge. A push that changes only `db/prod.sql` builds `-db-prod` and `-all`
   and deploys in about half an hour, with no Hugging Face upload. See
   [CI](ci.md).

Do not point `LEBLEG_TEST_DSN` at a `-db-prod` database: the test fixture runs
`db/prod.sql` and would drop the real tables.

## The changes

| Date | Change | Pull request |
| --- | --- | --- |
| 6 Nov 2026 | Cursor paging and list indexes, no OFFSET | [#173](https://github.com/taktekhq/lebanese-legislation/pull/173) |
| 7 Nov 2026 | Search in PostgreSQL alone, word counts from the build | [#176](https://github.com/taktekhq/lebanese-legislation/pull/176), [#177](https://github.com/taktekhq/lebanese-legislation/pull/177) |
| 9 Nov 2026 | `-db-prod`: cards and `possibly_affected_t` | [#188](https://github.com/taktekhq/lebanese-legislation/pull/188) |
| 9 Nov 2026 | `law_link` and one-pass JSON | [#192](https://github.com/taktekhq/lebanese-legislation/pull/192) |
| 9 Nov 2026 | Search: `search_known`, no count | [#193](https://github.com/taktekhq/lebanese-legislation/pull/193) |
| 9 Nov 2026 | Search word lists | [#194](https://github.com/taktekhq/lebanese-legislation/pull/194), [#195](https://github.com/taktekhq/lebanese-legislation/pull/195) |
| 9 Nov 2026 | `law_pub_count` | [#199](https://github.com/taktekhq/lebanese-legislation/pull/199) |

The project journal tells the same story for readers:
[the build-time tables](https://lebaneselegislation.com/en/blog/answers-prepared-at-build-time/)
and [the search word lists](https://lebaneselegislation.com/en/blog/search-ranks-from-build-time-word-lists/).
