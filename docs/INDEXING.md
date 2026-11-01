# Indexing

## Hubs and link depth

Every page the sitemaps list should also be reachable by links, in few clicks.
`cmd/linkdepth` measures it (see its README): it crawls the listing pages from
the home page and says how deep a sample of sitemap addresses sits.

Measured on 1 Nov 2026, 300 addresses from the sitemaps (same list before and
after, the "before" on the live site and on a local build of `main`, which agree
to the page):

| | within 3 clicks | at 5 clicks | deepest | median in-links |
|---|---:|---:|---:|---:|
| before | 98 of 300 | 120 | 5 | 2 |
| after | 124 of 300 | 0 | 4 | 2 |

Every page was already reachable. What changed is how far down the long tail
sat: a text on page 12 of 2012 was behind the pager.

- `/laws/years/`, `/rulings/years/` and `/gazette/years/` link every page of
  every year (a `<details>` list of `/laws/year/2012/page/7/`), so a text on any
  page of its year is three clicks from the home page, an article four.
- `/laws/year/{year}/{law|decree|text}/` (and `/page/N/`) list a year's texts by
  the word in their address (`wordForKind`), 300 a page, newest first. The year
  page links them with counts, and a text links its own.
- A text links the text of its kind and year issued just before and just after.
- `/codes/{slug}/` (and `/page/N/`) list the articles of each of the 26 codes in
  order, 100 a page under their chapter headings, every one a plain link.
  `/codes/` links them, an article page links the hub page it is on, and the
  code's text links its hub. `/code/{slug}/` still redirects to the text.
- All of it is in `internal/site/hubs.go`; the hub pages are in the core
  sitemap (640 addresses a language) through `AddCoreSitemap`. No existing
  address changed (`TestHubsLeaveEveryOldAddressAlone`).
- Not done: the 29 topic pages already link their articles; a topic hub
  made of data would need a subject for each text, which the database does not
  have.
