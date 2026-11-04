# Changes

## 3-4 November 2026

- AI crawlers may read everything. `robots.txt` gives each named AI crawler (GPTBot, OAI-SearchBot, ChatGPT-User, ClaudeBot, Claude-SearchBot, Claude-User, PerplexityBot, Perplexity-User, CCBot, ShapBot, DuckAssistBot, MistralAI-User) a group that allows search, `/api/v1/`, `/mcp` and query strings; only `/changes/review/` stays closed. The site Worker no longer answers ShapBot with 429. The per-address limit on dynamic paths (30 requests per 10 seconds) is unchanged.
- Deploys start on their own. `static-site.yml` runs on pushes to main that touch the site's code, not only after a data change. [static-site-catchup.yml](../.github/workflows/static-site-catchup.yml) runs after each deploy and every two hours, starts a deploy when the live site is behind main and reruns a failed one once.
- Renders do less. Each render records which templates and UI strings each route used (`deps.json`), so a template, string or topic guide change renders only those pages; a Go change still renders everything. Plans of up to 5,000 paths run on one runner. Pages are stored as parts inside a shared layout frame, so a header or footer change uploads the frames instead of about 760,000 pages. The render database starts from the `-db` image tagged `data-<hash>` for its commit, else from `data/`. See [static export](static-export.md).
- Checks run once per pull request commit, without `data/`, in about 3 minutes (was 12 to 13). [db.yml](../.github/workflows/db.yml) bakes and smoke-tests the `-db` image only when `data/`, `db/` or a package built into it changes.
- Gazette scans are no longer hosted. A text read from Gazette scans shows its machine reading and links the original PDF on SaderLex (`legislation_scan.source_url`); old `/scan/...` links redirect there with a 301. The images carry no scans.
- [scripts/nightly.sh](../scripts/nightly.sh) is the data update, in public: scrape, rebuild `data/`, check, export, and open a data-only pull request. It judges amendments and reads citations with Gemini (agy) or Claude, keeps page change dates from the published `data/`, and `LEBLEG_SCRAPE=0` builds from the published archive. See [update the data](update-data.md).
- `vectors.yml` embeds chunks without a vector (`cmd/embed-missing`), at most $1 and 20,000 chunks a day, and still publishes nothing while a current chunk lacks one.
- 22 topic guides rewritten to open with the answer, with figures tables and worked examples taken only from the cited articles. `/privacy/` and `/terms/` are new and linked in the footer.
- README and dataset card show live counts from `/badge/`; `stats.yml` sends daily GitHub and Hugging Face counters to PostHog.
- The dataset card, `METHODS.md` and `SCHEMA.md` live in `.github/huggingface/`; [hf-card.yml](../.github/workflows/hf-card.yml) uploads them when they change.

## 2-3 November 2026

- Hetzner is gone. Every part of LebaneseLegislation.com runs on Cloudflare: static pages and ruling PDFs from R2, everything dynamic in the `-all` image on Cloudflare Containers, deployed blue/green by [origin.yml](../.github/workflows/origin.yml). The public MCP server at `/mcp` is the Go server in that container; the old MCP Worker with its D1 snapshot is removed.
- Use `:latest`. Every image (`-db`, `-api`, `-mcp`, `-all`, `-vectors`) gets a `latest` tag and a UTC date tag from the workflow that produces its inputs. Compose and the docs now pull `:latest`; the 0.x version tags stay published but get no updates.
- No more PostgreSQL dump releases. The data is the Parquet dataset on Hugging Face and the database baked into the `-db` image.
- The static build runs in 24 shards, each page rendered by exactly one shard, and renders only the pages whose data changed.
- Every page shows when it last changed, with `dateModified` in its JSON-LD; `updated_at` is in `/api/v1/` responses and the MCP tools. `/openapi.json` describes the API as OpenAPI 3.1.
- Weak pages are hidden from search engines: badly scanned rulings and machine English for texts few people read (`noindex, follow`, still linked). The generated topic pages are hidden from Google only (`<meta name="googlebot" content="noindex, follow">`), so Bing, which ChatGPT and Copilot search, can still index them.
- Changed pages go to IndexNow after every static render (Bing, Yandex, Naver, Seznam).
- One `compose.yaml`: `db`, `api`, `mcp`, and the all-in-one service under the `all` profile. `./setup` only writes `.env`; `./start` is gone.
- A daily workflow checks that the AI answer on the search page still works.

## 1-2 November 2026

- Cloudflare now serves the website: about 760,000 static pages from R2 bucket `lebleg-site`, rendered from `data/` by [static-site.yml](../.github/workflows/static-site.yml) with `cmd/static-export` in 8 parallel shards. The JavaScript Worker in `workers/static-site` also serves ruling PDFs from R2.
- Search pages, `/api/v1/`, filters and Gazette scans go to one Cloudflare Container with PostgreSQL 18.6, a baked-in database and the Go website server. [origin.yml](../.github/workflows/origin.yml) deploys it.
- Semantic vectors moved out of PostgreSQL into Cloudflare Vectorize index `lebleg-chunks`: 1,547,937 chunks, 768 dimensions, `gemini-embedding-001`. [vectors.yml](../.github/workflows/vectors.yml) runs `build` then `publish` and `vectorize`, using `cmd/export-vectors` and `cmd/vectorize-sync`. The vectors are openly published as `vectors/vectors.parquet` on Hugging Face; later syncs upload only changed vectors.
- The former private `taktekhq/lebaneselegislation` repository is folded into this one: code, scraper, build pipeline, website and MCP.

See [architecture](architecture.md) and the [vectors example](reference.md#vectors).

## Unreleased

- Data is distributed as [Parquet on Hugging Face](https://huggingface.co/datasets/taktekhq/lebanese-legislation) and the [`-db` and `-all` images](../README.md#images).

The website is now the code LebaneseLegislation.com runs, not a copy from 30 October.

- New on the website: the project journal (`/blog/`), topics on the home page, self-hosted fonts and the `-events` usage record with its `/ai/` sign-up form. The contribute page stays at `/contribute/`.
- `web/` takes the flags and settings the hosted site uses (`-events`, `-official`, `-ruling-files`, `-noindex`, `-auth`, `POSTHOG_KEY`, `LEBLEG_SEARCH_PROVIDER`, `LEBLEG_VECTOR_DSN`). See [`web/README.md`](../web/README.md#flags-and-settings).
- Browser analytics: the images set `ANALYTICS=off`, so a local copy sends nothing to Google Analytics or PostHog and its about page says so. The binary alone loads them, as LebaneseLegislation.com does.
- All three services compress responses, use at most 8 database connections with JIT off, and finish requests in hand for 8 seconds on stop.
- The images' defaults are unchanged: no rate limit, links to `http://localhost:8080`, no analytics. The binary alone defaults to the hosted site's (60 requests a minute, links to LebaneseLegislation.com).
- MCP `get_law` and API search keep their 0.4.0 behaviour. Searching for a calculated amount now needs a digit in the query.
- New commands: `cmd/usage-stats`, `cmd/journal-sitemap`, `cmd/searchindex`, `cmd/topicgen`, `cmd/check-english`.

The data tools are Go commands now, one per job; the Python package in `tools/` is gone.

- The version lives in `internal/release/VERSION`.

## 0.4.0

The API and MCP images now run the same Go code as the website, which is the code behind LebaneseLegislation.com and its public MCP server. Python stays for the data tools only.

- Layout: `web/`, `api/` and `mcp/` each hold a `main.go` and a Dockerfile; their shared code is `internal/`. The Python package moved from `server/` to `tools/` and keeps `restore`, `build`, `publish`, `seed` and `pipeline`.
- Search is faster: on the full 2026-10-30.2 database a keyword search answers in well under a second, where the Python API took about 5.5 seconds per query and could hit the reader's 10 second limit on a fresh restore.
- The API serves `/api/v1/` (the same routes as before) and `/healthz`. The Python-only `/search`, `/api/search` and the `/docs` page are gone: use `/api/v1/search/`. Search by meaning is `semantic=true` on `/api/v1/search/` and `semantic: true` on MCP `search_legislation`; its passages come back in `related`, and `select=true` (`select: true`) lets Gemini keep only the ones that answer the question.
- `calculated_current` on Civil Procedure articles is unchanged, and searching for a calculated amount (`5,000,000,000`, with or without separators) still finds its article, flagged `matched_calculated_current`.
- A query that cites an article ("المادة 86 من قانون اصول المحاكمات المدنية") puts that article first, flagged `cited`, as the website's search does.
- Search results come from the website's word search (all words, Arabic normalisation, stemming) instead of a raw substring match, so counts differ from 0.3.0; counts stop at 1,000. Resource routes (laws, articles, rulings, Gazette) return the same fields as 0.3.0.
- The MCP image listens on 8080 inside the container, as before; Compose maps it to 8001. Browser requests must still come from `MCP_ALLOWED_ORIGINS` or the same localhost address. `get_law` returns the full outline, as before, with optional `articles_offset` and `articles_limit`.
- Settings are unchanged (`GEMINI_API_KEY`, `GEMINI_MAX_CALLS_PER_DAY`, `GEMINI_CONCURRENCY`, `GEMINI_QUERY_CACHE_SIZE`, `GEMINI_QUERY_CACHE_TTL`, `MCP_ALLOWED_ORIGINS`). New: `GEMINI_SEARCH_MODEL`, `GEMINI_EMBEDDING_MODEL`, and `RATE_LIMIT_PER_MINUTE` and `RATE_LIMIT_BURST` for a per-client limit when you host the API or MCP publicly (off by default, as before).
- `lebleg restore` now adds what the services read (`tools/src/lebleg/sql/web.sql`) itself, so the update pipeline and your own PostgreSQL get it too.
- `./setup` writes `.env` itself; `lebleg init` and `the web server` are gone. Each Go image has a built-in health check (`/server healthcheck`).
- The handwriting demo left this repository. Its source is the [Hugging Face Space](https://huggingface.co/spaces/taktekhq/lebanese-legal-handwriting).

## 0.3.0

- The former standalone website image is retired. `./setup` now uses `ghcr.io/taktekhq/lebanese-legislation-all:latest` at http://localhost:8080 next to the API and MCP.
- The DB image adds the website serving schema ([`db/web.sql`](../db/web.sql)): three text functions, six generated search columns, two views, four empty tables and 24 indexes. It runs once after the first restore and adds a few minutes to it.
- The DB health check now also requires that step. On a database restored by 0.2.0 or older, run it once: `docker compose exec db bash /opt/lebleg/db/web-schema.sh`.

## 0.2.0

The repository now has one folder per part: [`db/`](../db/), [`server/`](../server/), [`web/`](../web/), [`examples/`](../examples/) and [`docs/`](./). API, MCP and data behave as in 0.1.7.

- The DB image builds PostgreSQL and pgvector itself from the official `postgres:18.6` image. It no longer depends on the deleted `lebanese-legislation-starter` image, so it builds again from a clean checkout.
- The Python package is now `lebleg` (was `lebleg_starter`). The `lebleg` command is unchanged.
- Inside the images, files moved from `/opt/starter` to `/opt/lebleg`. The DB restore script is `/opt/lebleg/db/release.py`.
- `compose.yaml` takes the DB health check from the image, so it works with any image tag.
- The Compose project is now `lebanese-legislation` (was `lebleg-starter`). Its volume name changes with it. To keep a database restored by 0.1.x, set `COMPOSE_PROJECT_NAME=lebleg-starter` in `.env`.

## 0.1.6

Calculated-text lookup is restricted to original Arabic articles of Civil Procedure law 244565. Other sources still match their original titles and text. This bound lets PostgreSQL use the existing source key and trigram indexes for literal amount searches. No dataset or embedding change is required.

The [image workflow](https://github.com/taktekhq/lebanese-legislation/actions/workflows/images.yml) builds and checks DB, API and MCP for AMD64 and ARM64 before promoting their immutable version tags. A retry reuses a version only when it belongs to the same source commit. Existing versions are never replaced.

## 0.1.4

General resource routes retain the v1 response fields. Search uses the published snapshot's derived corpus, including its release-date/in-force article selection; raw resource routes retain historical records. `/api/v1/search/` defaults to exact matches and reports exact category counts. `semantic=true` adds related passages and reports returned-item counts; semantic pagination is first-page only. MCP uses that same implementation. Source scan markup and headings are returned unchanged.

The MCP `latest` alias moves to the consolidated backend. Earlier Go image digests and `sha-*` tags remain available. DB/API/MCP version 0.1.4 and later expect the verified public snapshot schema and restore receipt. The older standalone website remains buildable with its existing SQL search behavior.

Release workflows first build source-tagged candidates and check both CPU architectures. They then promote immutable version tags and update `latest`. A retry from the same source commit skips an already-promoted image and retains its digest; a different source commit cannot replace that version.

The initial restore can take longer than a normal database boot. DB health has a two-hour startup grace period and becomes healthy as soon as the verified restore finishes. Set `DB_START_PERIOD` and `STARTUP_TIMEOUT` for a slower machine. This does not change or refresh an existing volume.
