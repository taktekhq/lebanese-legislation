# Changes

## 0.3.0

- New image: `ghcr.io/taktekhq/lebanese-legislation-web`, the website from `web/`. `./start` runs it at http://localhost:8080 next to the API and MCP.
- The DB image adds what the website reads and the public dump lacks ([`db/web.sql`](../db/web.sql)): three text functions, six generated search columns, two views, four empty tables and 24 indexes. It runs once after the first restore and adds a few minutes to it.
- The DB health check now also requires that step. On a database restored by 0.2.0 or older, run it once: `docker compose exec db bash /opt/lebleg/db/web-schema.sh`.

## 0.2.0

The repository now has one folder per part: [`db/`](../db/), [`server/`](../server/), [`web/`](../web/), [`examples/`](../examples/) and [`docs/`](./). API, MCP and data behave as in 0.1.7.

- The DB image builds PostgreSQL and pgvector itself from the official `postgres:18.6` image. It no longer depends on the deleted `lebanese-legislation-starter` image, so it builds again from a clean checkout.
- The Python package is now `lebleg` (was `lebleg_starter`). The `lebleg` command is unchanged.
- Inside the images, files moved from `/opt/starter` to `/opt/lebleg`. The DB restore script is `/opt/lebleg/db/release.py`.
- `compose.yaml` takes the DB health check from the image, so it works with any image tag.
- The Compose project is now `lebanese-legislation` (was `lebleg-starter`). Its volume name changes with it. To keep a database restored by 0.1.x, set `COMPOSE_PROJECT_NAME=lebleg-starter` in `.env`.
- The PostgreSQL dump is linked only from Hugging Face. The GitHub data release is gone; GitHub releases are code versions.

## 0.1.6

Calculated-text lookup is restricted to original Arabic articles of Civil Procedure law 244565. Other sources still match their original titles and text. This bound lets PostgreSQL use the existing source key and trigram indexes for literal amount searches. No dataset or embedding change is required.

The [image workflow](https://github.com/taktekhq/lebanese-legislation/actions/workflows/images.yml) builds and checks DB, API and MCP for AMD64 and ARM64 before promoting their immutable version tags. A retry reuses a version only when it belongs to the same source commit. Existing versions are never replaced. Release 2026-10-30.2 retains its truthful builder version 0.1.5; runtime 0.1.6 consumes that same verified dump.

## 0.1.4

General resource routes retain the v1 response fields. Search uses the published snapshot's derived corpus, including its release-date/in-force article selection; raw resource routes retain historical records. `/api/v1/search/` defaults to exact matches and reports exact category counts. `semantic=true` adds related passages and reports returned-item counts; semantic pagination is first-page only. MCP uses that same implementation. Source scan markup and headings are returned unchanged.

The MCP `latest` alias moves to the consolidated backend. Earlier Go image digests and `sha-*` tags remain available. DB/API/MCP version 0.1.4 and later expect the verified public snapshot schema and restore receipt. The older standalone website remains buildable with its existing SQL search behavior.

Release workflows first build source-tagged candidates and check both CPU architectures. They then promote immutable version tags and update `latest`. A retry from the same source commit skips an already-promoted image and retains its digest; a different source commit cannot replace that version.

The initial restore can take longer than a normal database boot. DB health has a two-hour startup grace period and becomes healthy as soon as the verified restore finishes. Set `DB_START_PERIOD` and `STARTUP_TIMEOUT` for a slower machine. This does not change or refresh an existing volume.
