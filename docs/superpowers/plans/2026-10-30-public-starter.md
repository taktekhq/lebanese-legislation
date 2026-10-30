# Public database and starter implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement these independently owned components, with scoped and final review.

**Goal:** Publish a tested public PostgreSQL/pgvector snapshot and reusable Docker starter while retaining current Parquet exports.
**Architecture:** Build only from a pinned public HF snapshot; validate/reuse compatible vectors, export a logical dump and manifest; consumer restores into a fresh local database and uses read-only source-grounded retrieval.
**Tech Stack:** Python3.11+, psycopg3, FastAPI/uvicorn, optional PyArrow/HF SDK publisher extra, PostgreSQL18.6/pgvector0.8.2, Docker Compose.
**Spec:** docs/superpowers/specs/2026-10-30-public-starter-design.md

## Global constraints

MIT code copyright Taktek and Nizar Mahmoud; dataset retains its own attribution/licence. Keep29Parquet exports intact. No keys/private queries/private provenance in releases. 768-dimensional halfvec,900code-point chunks,120overlap. Preserve source wording and unofficial English labels. Source/model/version matching mandatory. Full coverage required for public ready release; paid fill explicit and bounded. PostgreSQL18.6/vector0.8.2, pinned source checksums. Refuse automatic destruction/overwrite of existing databases. Loopback-only published ports. Same source-ID quotation validation as the website.

## Review focus

- An interrupted or wrong-version restore must not silently replace someone's existing database.
- Reused private vectors must match a source actually present in the public pinned snapshot; no website-only explanations leak into artifacts.
- Download paths, manifest fields and SQL identifiers cannot become arbitrary filesystem writes, shell execution or exposed credentials.
- Model mismatch, stale hashes, Unicode offsets and unknown source IDs must fail safely.
- A public artifact must restore with the public Docker image and reader role, without owner credentials or API keys exposed to clients.

## Tasks

- [ ] Publisher/data engine: tests first for types, eligibility, chunks, hash/offset reuse, coverage and cap; implement public Parquet import/source documents, vector reuse/fill and verified dump/manifest export. Owner:data engineer.
- [ ] Consumer/runtime: tests first for manifest checksum/path/model errors, occupied target/idempotence, read-only retrieval and provider ID validation; implement restore/download, CLI and search API. Owner:runtime engineer.
- [ ] Container/publishing: public image from exact pinned PG18.6 base, Docker startup/roles, CI, licence/README and HF immutable release publisher; verify real fresh-container restoration. Owner:parent.
- [ ] Integrate/review: run full fixtures/integration tests; independent scoped reviews; build full public snapshot, reuse/dry-run then explicit capped fill if needed; independently restore full artifact and run real-query smoke.
- [ ] Release/adoption: publish public image, GitHub code/tag/release, HF dump/manifest/latest/card; preserveParquet; integrate shared tooling/image reference into our existing workflow through reviewed PR; green checks, public anonymous download and fresh starter verification, cleanup and committed records.

## Shared module contract

Package src/lebleg_starter/. Data engineer owns build.py,sources.py,chunks.py and their tests plus sql/public_sources.sql. Runtime engineer owns manifest.py,restore.py,embedding.py,search.py,server.py and tests. Parent owns pyproject/CLI wiring, Docker/Compose, publisher upload integration, docs and release. Shared schema follows the spec; coordinate schema/manifest changes with parent. No implementation agents edit each other's files or publish externally. Parent handles provider keys only in process memory and all external publication.

## Ledger

User approved the public package already described in chat; proceed without another permission round. Builder review found no material missing decisions. Full coverage versus optional paid fill was clarified: report incomplete reuse without publishing, then explicitly fill within cap if appropriate. Existing publicdata is29Parquet files (~345MB), pinned snapshot7d29da2e61cc921ceb75430c6a62d5ef0489e574. Public ready artifact must use this exact revision or an explicitly recorded newer revision.
