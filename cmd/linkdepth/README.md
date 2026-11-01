# linkdepth

How many clicks does it take to reach a page, by following only plain links
from the home page? `linkdepth` answers that for a sample of the addresses a
site's sitemaps list.

A page that only the sitemap lists is a page a search engine has to be told
about. A page five clicks down is crawled after the pages above it. This tool
puts a number on both.

```
go build -o linkdepth ./cmd/linkdepth
./linkdepth -base https://example.com -sample 300 -rps 5 -out report.md
```

It reads `/sitemap.xml`, takes an even sample of addresses from a few of the
files it lists, crawls the site's listing pages at a polite rate with its own
`User-Agent`, and prints one row per kind of page: how many were reached within
3 and within 5 clicks, how many were never reached, how many have no in-link
from a crawled page, and the median number of in-links.

To compare two builds, write the paths of one run to a file (`-json` saves
them) and give both runs the same list with `-urls`.

## What it does and does not follow

- Only `<a href>` on the same host. Links with a query string are dropped
  (a `robots.txt` that disallows `/*?` makes a crawler skip them too), as are
  fragments, files and feeds.
- Redirects are not followed: a link to a moved page is a link to a page that
  has moved.
- It expands every **hub** (the home page, lists, years, pagination, topics,
  codes) because those are few and carry the links. It expands a text page only
  when it is the parent of a sampled page (a law page and its parts). A site
  with 400,000 article pages cannot be fetched politely, so the other pages are
  leaves of the crawl.

So a depth is the depth by hub navigation plus the sampled page's own parent.
Real crawlers also follow links between texts (related, amended by), so the
true depth is the same or smaller, never larger. "Not reached" means not
reached that way, not that no page links it.

`-hub` takes a regexp for another site's listing pages. The default matches
lebaneselegislation.com. `-max-pages` stops a run that is too large.

## Licence

MIT.
