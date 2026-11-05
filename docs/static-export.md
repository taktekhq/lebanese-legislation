# Static export of lebaneselegislation.com

The site is served by Cloudflare. About 760,000 static pages are rendered from
`data/` by [`cmd/static-export`](../cmd/static-export/README.md), using the
website's own handlers and templates.

The [static-site workflow](../.github/workflows/static-site.yml) runs 24 parallel
shards and uploads the rendered files to the R2 bucket `lebleg-site`. The
JavaScript Worker in [`workers/static-site`](../workers/static-site/) serves
them. The same Worker serves ruling PDFs from R2.

Search pages, `/api/v1/`, filters and Gazette scans go to one Cloudflare
Container. The [origin workflow](../.github/workflows/origin.yml) deploys it
from [`workers/static-site/container/Dockerfile`](../workers/static-site/container/Dockerfile),
with PostgreSQL 18.6, a baked-in database and the Go website server.

Semantic vectors live in Cloudflare Vectorize index `lebleg-chunks` and are
published as `vectors/vectors.parquet` on Hugging Face by the
[vectors workflow](../.github/workflows/vectors.yml).

See [architecture](architecture.md) for the full request and build paths,
and the [command README](../cmd/static-export/README.md) for local export,
comparison and upload instructions. This replaces the pre-deployment spike
notes from 1 November 2026.

## Render the static site yourself

Run the published database image, then export from a checkout of this repository:

```sh
docker run -d --name lebleg-db -e POSTGRES_PASSWORD=local \
  -p 127.0.0.1:5432:5432 ghcr.io/taktekhq/lebanese-legislation-db:latest
# Wait until the database is healthy before exporting.
export LEBLEG_DSN='postgresql://postgres:local@127.0.0.1:5432/lebleg'
go run ./cmd/static-export export -out site -workers 4 \
  -base https://example.org -search-shell
```

A full export takes about 3 to 4 hours on a 4-core machine: about 760,000 pages
at about 60 pages/s. Any web server can serve the files in `site/`. Configure its
404 response to use `404.html`. The exporter also writes `.export/redirects.json`:
the Cloudflare Worker uses this data at `_export/redirects.json` to resolve old
addresses. A mirror can implement the same redirects in its web server. Search
with `-search-shell` needs an API, configured through `LEBLEG_API_BASE` before export.

## Changed-page renders in CI

CI still loads PostgreSQL directly from the checkout's `data/` using `data/load.sh`.
It does not pull the `-db` image or wait for that image to build. The example above
is an independent option for people rendering locally.

The workflow reads `_export/rendered-commit` from R2 and pins the current checkout
for every job. `static-export changes -since COMMIT -out plan` reads the added and
removed COPY-text rows from `git diff data/tables`. It maps both old and new
memberships to law, decree, text, article, ruling, Gazette and document paths,
including their English twins. Graph edges, references, amendments, Gazette
entries and agreements identify linked pages. Related laws in the same year and
address kind, affected year lists, topic and code hubs, feeds, home, changes and
sitemaps are refreshed too. List pagination is refreshed as a group when its
membership or labels change, so insertions and removals cannot leave stale pages.

Each shard exports the planned list with `-paths`, using the existing `Owner`
function. A changed-only plan of at most 5,000 paths (`SMALL_PLAN`) runs on one
runner instead of 24: the database loads once and the 24 shards run one after
another, each with its own manifest, so the result is the same as the matrix.
The plan job asks the live Worker once whether it puts pages together
(`-edge-layout on` or `off` for every shard). Run workflow also takes `paths`, a
space-separated list of pages to render now; it leaves `_export/rendered-commit`
where it is. This does not crawl the site. It discovers only new parts of selected
long laws, and includes old parts and list pagination from its manifest to remove
obsolete files. Unvisited manifest entries are retained. Removed pages and pages
that become redirects are deleted from R2 by their owner. Full renders retain the
existing R2 path exchange for crawl discovery.

A missing baseline, `workflow_dispatch` with `full=true`, or a renderer change
forces a full render. The renderer is every Go and embedded file (templates,
strings, topics, assets) of the packages `cmd/static-export` and `cmd/build-db`
are built from, as `go list -deps` reports them, plus the inputs the workflow
loads (`db/official.sql`, `data/load.sh`, the schema files). Tests and notes beside
that code change nothing.

Templates, UI strings, topic guides, the journal and assets are the exception: they are mapped
to the pages they touch, and a change to Go code still renders everything. Every
render leaves a record beside each shard's manifest, `deps.json`: for each route,
the page templates its pages executed and the UI strings Go code looked up for
them (`site.RecordDeps`, noted in `execute` and in `s.bundle`). The planner
(`static-export changes -r2-bucket`, `internal/staticexport/content.go`) reads all
24 records and manifests and maps:

- a template file to the routes whose page templates reach it (it parses the
  templates from `layout.html` and the blocks Go executes by name);
- a changed string to the routes whose Go code looked it up, and the routes whose
  templates use it, directly, through `printf`, or inside a template function
  (it reads the `FuncMap` entries and the functions they call, i18n included);
- a topic guide to its own pages and the article pages it quotes or stopped
  quoting; a changed title, question or set of guides adds every topic page, the
  topic index and the home pages;
- the journal (`internal/site/blog.json`) to every `/blog/` page in the manifests,
  each entry before and after the change, and the sitemaps that list or date
  them (`/blog-sitemap.xml`, `/sitemaps/core.xml`, `/sitemaps/ar-core.xml`,
  `/sitemap.xml`): a new entry is a small plan, not a full render;
- an asset to no page: the run uploads the assets (`?v=` is the fixed build id).

A string in the shared layout, a partial most pages reach, a missing record or
more than half the pages renders everything. `plan/why.txt` and the run summary
say what was decided and why. On PR #120's 22 guide edits, the guide rule planned
every one of the 116 article pages the full render changed in the three shards
checked, at about two planned pages per changed one. Every Sunday at 02:41 UTC
the workflow renders everything regardless, so a page a missed dependency left
stale is put right within a week; that run's `indexnow` line counts the pages it
changed.

On a pull request that touches what the site is built from,
[deploy-preview.yml](../.github/workflows/deploy-preview.yml) runs the same plan
from the live site's commit to the pull request merged into main, and comments
what merging it would render and why. It renders nothing. The one exception is a change to the shared frame of
`internal/site/templates/layout.html` alone (see below): it renders only the
two search pages, which are stored whole. Schema, rendering dependencies and unmapped table changes
also fall back to a full render. The workflow serializes runs and keeps 24 owners,
matching the stored manifests. Only after all shards and the generation job
succeed does the final job write `_export/rendered-commit`. A failed run leaves
the baseline unchanged, so its changes are retried. A plan with nothing to render
writes it straight away.

Deploys start on their own. A data change deploys after the data workflow has
checked it (`workflow_run`). A push to main that changes code the site is built
from deploys straight away; one that also changes `data/` waits for the data
workflow. When a deploy on main finishes, and every two hours as a backstop,
[static-site-catchup.yml](../.github/workflows/static-site-catchup.yml)
compares `_export/rendered-commit` with main and starts a deploy when the site is
behind and no deploy is queued or running, once the newest data change has passed
the data workflow (its build job passed). A failed deploy runs once more, all of
it: a full render's shards exchange crawl paths per run attempt, so one shard
rerun alone cannot finish. A second failure turns the check red.

## Edge layout: one frame for every page

Every page wraps its own content in `layout.html`: the head, the header and
menu, the footer, the analytics tags. Stored whole, a one-word change to the
footer changes the bytes, and so the hash, of all 760,000 pages, and the next
run uploads every one of them again: about 760,000 R2 writes (Class A, about
$3.40 at $4.50 a million) after a 3 to 4 hour full render. With the edge
layout the same change uploads two small objects.

**Frame and parts.** `layout.html` is a frame: it may use only what is the
same on every page of a language (`t`, `u`, `uilang`, `assetVersion`,
`analytics` and the like). Everything it takes from the page goes through
the `page:` templates of `_page.html`: title, description, canonical,
hreflang, robots, `og:type`, the Arabic text font, JSON-LD, the two language
links, and the content. `internal/site/layoutframe.go` renders the frame of
each language with no page at all (a field used outside a `page:` template
fails there) and a mark in each hole, which gives the pieces of shared HTML
between the holes. Shared blocks the content uses (`lawrow`, `article`,
`pager`...) moved to `_blocks.html`, so they count as page content.

**Stored form.** The exporter cuts each rendered HTML page at the pieces of
its frame and stores only what lies between them, after checking that the
pieces and the parts give back the rendered page byte for byte:

```
LLP1 <lang> <schema> <len>,<len>,...\n<part><part>...
```

The manifest hash covers this object only. The frame of each language is an
immutable `_layout/<id>.json` (`id` from its SHA-256), and
`_export/layout.json` names the frame for each `lang/schema`. The schema
names the holes and how each one is escaped where it stands (a probe string
is printed into each hole), so a page cut under one schema fits every frame
of the same schema. Older schemas stay listed (four per language) for pages
not yet cut again. Sitemaps, feeds, `robots.txt` and the two search pages
(their script is added before `</body>`, in the frame) are stored whole. Any
other HTML page that does not fit the frame stops the export, because a
frame-only change would leave it with the old footer.

**At the edge.** The Worker (`workers/static-site/src/layout.js`) reads the
object; when it starts with `LLP1 ` it fetches the frame named by
`_export/layout.json` (read once a minute, frames kept in memory) and joins
the bytes. Anything else, including every page stored before the change, is
served as it is. A page in parts whose frame cannot be found goes to the
origin, never out as parts. The page cache key carries the export generation
and the ETag of `_export/layout.json`, so a new frame shows at once; with no
`_export/layout.json` the key is what it was before. The page's ETag gains
the frame id. Analytics tags (`ga.js`, `load.js`, `data-ph`) are in the frame,
so every page served carries them; `TestLayoutFramesRebuildPages` checks it.

**What a change costs now.** `static-export changes` compares the schemas of
`layout.html` at both commits. A change to the frame alone (header, footer,
menu, analytics tags, stylesheet links) is not a renderer change: the plan
holds the two search pages, the run stores the new frames and
`_export/layout.json` before any page (only shard 0 writes them), and uploads
no other page. That is 2 frames, the pointer and 2 pages, plus the support
files and manifests every run writes. A change that adds, removes or moves a
hole, touches `_page.html`, `_blocks.html`, a page template, the i18n
strings or Go code still renders everything; even then, a page whose parts
did not change has the same hash and is not uploaded.

**Byte for byte.** `TestEdgeLayoutThroughWorker` (cmd/static-export) exports
the pages that need no database whole and in parts and runs the Worker's own
code (`test/edgelayout.test.mjs`) over a bucket made of the parts. On a corpus
sample the same test compared 3,284 HTML pages, Arabic and English, of every
kind (laws, decrees, texts and their articles, rulings, Gazette issues, year
and pager lists, codes, opinions, agreements, doctrine, research, home,
about, data, changes), rendered whole by the main branch's exporter, with what
the Worker answers from this exporter's parts: all 3,284 identical, 3,282 of
them stored in parts. Stored bytes fell from 119.5 MB to 84.1 MB. To repeat it:

```sh
static-export-main export -out proof/whole -paths paths.txt -crawl=false -search-shell
static-export      export -out proof/parts -paths paths.txt -crawl=false -search-shell -edge-layout on
cd workers/static-site && EDGE_LAYOUT_DIR=../../proof node --test test/edgelayout.test.mjs
```

### Cutover

1. Merge. `-edge-layout auto` (the default with `-r2-bucket`) asks the live
   Worker for `/_build/layout`; the old Worker answers 404, so pages are still
   stored whole. The merge touches `internal/site`, so the next run is a full
   render, but the whole pages are byte for byte the same, so it uploads no
   page.
2. Deploy the Worker (origin workflow, `deploy_site_worker`). It serves the
   whole pages as before, with the same cache keys while
   `_export/layout.json` does not exist.
3. Run the static-site workflow with `full=true`. The exporter now finds the
   Worker, writes the frames and `_export/layout.json`, then stores every HTML
   page in parts: a one-time rewrite of every page, as any full render was
   before. During the run some pages are whole and some in parts; the Worker
   serves both.
4. Optional: pass `-edge-layout on` in the workflow, so a run never depends on
   reaching the live site.

To roll the Worker back to a version without the edge layout, first run a
full render with `-edge-layout off` (pages stored whole again), then roll it
back. An older Worker would serve pages in parts as they are.
