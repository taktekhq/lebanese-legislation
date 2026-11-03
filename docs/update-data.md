# Update the data yourself

Run the nightly on your own machine, in a dedicated checkout. It refuses to run
if `GITHUB_ACTIONS` is set, even to an empty value. Maintainers review and merge
the data PR. The workflows then publish the dataset, website, MCP and images.
The script does not deploy anything.

## Requirements

- Linux with Bash, `flock` (util-linux), Git, rsync, curl, Python 3 and Go
  matching `go.mod`. The scraper uses Python's standard library.
- A local PostgreSQL database, with permission to recreate its `public` schema
  and install `pg_trgm`. Use a dedicated database: **build-db drops the public
  schema and rebuilds it**. Set `LEBLEG_DSN` to its connection string. The
  default is `host=localhost dbname=lebleg`; normal PostgreSQL authentication
  settings such as `.pgpass` apply.
- GitHub CLI (`gh auth login`), Git commit identity and push access to the
  configured remote and target repository. For a fork, set `LEBLEG_REMOTE` to
  your fork remote, `LEBLEG_REPO` to the upstream and `GH_REPO` is not needed.
  Cross-repository PRs require `LEBLEG_HEAD_OWNER` as described below.
- Disk space for the raw store, archive submodule, PostgreSQL and an additional
  checkout of `data/`. The first complete crawl can take days. Use the scraper's
  resumable `backfill` and `articles` commands before expecting a full rebuild;
  see [scraper/README.md](../scraper/README.md). Keep its six-worker default.
  The build gates can reject an incomplete first crawl.

```bash
git clone --recurse-submodules https://github.com/taktekhq/lebanese-legislation.git
cd lebanese-legislation
createdb lebleg
scripts/nightly.sh --dry-run
scripts/nightly.sh
```

`--dry-run` still scrapes, rebuilds PostgreSQL, exports and commits the data to
`data/YYYY-MM-DD`. It only skips the push and PR. It leaves that local branch
for review and removes its temporary worktree. Before running again that day,
review and delete that local branch (`git branch -D data/YYYY-MM-DD`), or use
another date on the next night. Normal runs also refuse an existing local branch
rather than overwrite a previous run. No code from your working branch is
included in the data commit.

## Configuration

Paths should be absolute when supplied; defaults below are relative to the
checkout root. Add Go and PostgreSQL tools to `PATH` when using a scheduler.

| Variable | Default | Purpose |
| --- | --- | --- |
| `LEBLEG_STORE` | `scraper/data` | Resumable scraped store, logs and local step marks |
| `LEBLEG_DSN` | `host=localhost dbname=lebleg` | Dedicated rebuild database |
| `LEBLEG_DATA` | `data` | Export destination; the checked-in shard plan seeds an empty destination |
| `LEBLEG_ARCHIVE` | `archive` | Raw archive for scraper export, build and wording guard |
| `LEBLEG_SCANS` | `manual/scans` | Optional Gazette bundle, with its adjacent manifest |
| `LEBLEG_RULINGS` | `manual/rulings` | Optional ruling readings and index |
| `LEBLEG_RULING_PDFS` | same as `LEBLEG_RULINGS` | PDFs for the advisory ruling check |
| `LEBLEG_LOCK_FILE` | `$LEBLEG_STORE/nightly.lock` | Lock held throughout the run |
| `LEBLEG_LOCK_WAIT` | `3600` | Seconds to wait, then exit 75 if still locked |
| `LEBLEG_REPO` | `taktekhq/lebanese-legislation` | Target repository for `gh pr create` |
| `LEBLEG_REMOTE` | `origin` | Git remote fetched and pushed; configure a fork remote if needed |
| `LEBLEG_BASE` | `main` | Base branch for the clean data worktree and PR |
| `LEBLEG_HEAD_OWNER` | unset | Fork owner, to create the PR from `owner:data/YYYY-MM-DD` |
| `LEBLEG_TIMEZONE` | `UTC` | Time zone for the date in the branch name |
| `LEBLEG_SCRAPE_MINUTES` | `180` | Daily scraper budget |
| `LEBLEG_ARTICLE_MINUTES` | `30` | Full-text scraper budget |
| `LEBLEG_AGY` | empty | Path to signed-in agy CLI; empty disables Gemini reading |
| `LEBLEG_CLAUDE` | empty | Path to signed-in Claude CLI; empty disables Claude reading |
| `LEBLEG_AI_REMOTE` | empty | Optional SSH host where both CLI paths run; empty runs locally |
| `LEBLEG_JUDGE_MODEL` | `gemini-4-argon-medium` | Gemini model for judge and citation reader |
| `LEBLEG_CLAUDE_MODEL` | `sonnet` | Claude model for judge and citation reader |
| `LEBLEG_OMIT` | unset | Optional steps to omit: `amendments,versions,rulings` |
| `LEBLEG_OMIT_FILE` | `$LEBLEG_STORE/omit-next` | Same omissions, one ID per line, consumed once |
| `LEBLEG_OMIT_ONCE_FILE` | under the store logs | Snapshot of that one-night omission file |
| `LEBLEG_DNS_SKIP` | `0` | Set to `1` to skip the DNS wait |
| `LEBLEG_DNS_WAIT` | `10800` | Maximum DNS wait in seconds |
| `LEBLEG_DNS_INTERVAL` | `900` | Seconds between DNS attempts |
| `LEBLEG_DNS_LOOKUP` | system resolver | Optional executable taking a hostname |
| `LEBLEG_DNS_TEST_ONLY` | `0` | Set to `1` to stop after testing DNS |

The advisory data-age helper accepts `HF_REPO`, `CHECK_DATA_HF_API`,
`CHECK_DATA_MAX_AGE` (108000 seconds), and `CHECK_DATA_NOW` (epoch seconds).
It only reads public Hugging Face metadata. It never uploads anything.

## What runs

1. Take the lock and arrange exit and signal reporting. Local logs live in
   `$LEBLEG_STORE/logs/nightly.log`, with the build report and per-check logs
   beside it. Wait for DNS and warn if the published data is stale.
2. Update the archive submodule, run the existing `ul-scrape daily` and
   `articles`, then export raw pages into the local archive. There is no archive
   push. An unavailable submodule update falls back to the existing archive,
   as in the original nightly.
3. Build the public Go commands. Record the scan manifest, then run `build-db`.
   Preserve the original build-report, date and amount gates, and advisory
   English, version and ruling checks. Local bundles replace remote transfers.
   Checked-in model judgements and citations are loaded. After the build gates
   pass, step 7 judges amendments, then reads article citations with the configured
   agy CLI. A quota or limit failure falls back to Claude when configured;
   citation exit 3 also triggers fallback. With only Claude configured, both
   commands use it directly. With neither CLI configured, step 7 is marked skipped.
   `LEBLEG_OMIT=amendments` omits both commands without a mark. Only new inputs
   are read. Failures retain partial answers and never block export. Changed
   judgements rebuild amendments; either changed model file rebuilds the graph
   before indexing and export.
4. Always run `build-db -only indexing`, using `data/popular_texts.txt`.
   A missing list or failed indexing stops export. This keeps `noindex_page`
   rows of kind `english` required by [issue #99](https://github.com/taktekhq/lebanese-legislation/issues/99).
5. Export sorted tables, including `noindex_page`, and calculated current
   wording. Both exports keep the independent provenance gate. Shard plans
   preserve stable IDs and file ranges. A blocked gate stops publication.
6. Fetch the base branch into a clean temporary git worktree. Mirror only the
   generated data files, including removed table shards; keep curated files
   such as `popular_texts.txt`, README and `load.sh`.
   Copy `db/model/judgements.jsonl` and `db/model/citations.jsonl` into the same
   worktree and stage them with `data/`. If neither changed, stop. Otherwise commit
   `data/YYYY-MM-DD` with the `data/` diff summary and restore the clone's model files from Git after
   committing, in both dry and real runs. Retry a failed push up to three times,
   and open a PR. Cleanup removes the worktree even on failure. The local branch and commit remain available for recovery.

This retains the private nightly's order and error handling for the surviving
steps. `flock` replaces its private pipeline lock, and local marks replace
private pipeline reporting. The imported helpers are `check-build-report.sh`
and `check-data-age.sh`. All other retained operations use this repo's commands.
Hetzner delivery, D1/MCP snapshots, Hugging Face upload, private repository
pushes and private notifications are absent.

## First-run checks from issue #99

Before merging the first data PR:

- Inspect the `data/` diff. It should be small, with stable IDs. Investigate a
  large rewrite before merging.
- Confirm the exported `data/tables/noindex_page/` retains `english` rows.
- Render or serve the rebuilt database locally. Verify
  `/en/decree/193806/` still declares `noindex, follow`, and the three
  `/sitemaps/laws-1.xml`, `laws-2.xml`, `laws-3.xml` contain 307 URLs total.
  `cmd/static-export export -paths FILE -crawl=false -db "$LEBLEG_DSN" -out DIR`
  can render those four paths without deploying or running a workflow.
- Check that the data PR passes the repository checks. Maintainers merge it;
  publishing happens through the workflows after merge.

## Nightly user timer

Examples are in [systemd/lebleg-nightly.service](systemd/lebleg-nightly.service),
[systemd/lebleg-nightly.timer](systemd/lebleg-nightly.timer) and
[systemd/lebleg-nightly.env.example](systemd/lebleg-nightly.env.example).
Edit the service checkout path and environment file for your machine. Copy the
units to `~/.config/systemd/user/`, save the environment file as
`~/.config/lebleg/nightly.env` with mode 600, then run:

```bash
systemctl --user daemon-reload
systemctl --user enable --now lebleg-nightly.timer
journalctl --user -u lebleg-nightly.service
```

The timer uses the machine's local time. Run `loginctl enable-linger` if your
system requires it for user timers to run while logged out.

## Check script changes locally

Run `python3 scripts/nightly_test.py` for isolated tests using temporary local
Git repositories and command stubs. They exercise the build/indexing/provenance
gates, lock contention, dry runs, data-only commits, deletions, worktree cleanup,
no-change runs and the PR arguments. They do not scrape or publish externally.
