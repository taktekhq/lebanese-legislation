package site

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

// What decides whether a page is offered to search engines, and where list
// pages paginate. The sitemaps follow the same rules (seo.go).
//
//   - A text's own page (law, decree, text, and the parts of a long one) and
//     each of its articles have an English twin of their own: both are
//     self-canonical with an hreflang pair (ar and x-default = root, en =
//     /en/), whatever the text's kind. The other pages under a text (a
//     ruling, a gazette issue, a document) are the same Arabic text in both
//     interfaces: the /en/ address names the root (Arabic) one as canonical
//     and the pair has no hreflang. Page.Translated marks the pages that are
//     pairs (texts, articles, the principal codes, the topic pages).
//     /ar/ is not a page: it answers 301 to the root (lang.go), in one hop
//     to wherever the address has moved since (redirect.go).
//   - A page with nothing to index (noindex_page, written by the build) is
//     `noindex, follow`: reachable and linked, not offered.
//   - Weak pages are hidden the same way (issue #86, internal/indexing/weak.go):
//     every court ruling page and list of rulings (rulingsHidden); the
//     /en/ pages of every text not in data/popular_texts.txt (kind english:
//     the text page, its parts and its articles; the Arabic pages stay
//     open); and every generated topic page, /topics/ and /topic/<slug>/ in
//     both languages (topics.go). The English home, list pages, code and
//     year hubs and info pages stay open.
//   - The article pages of a short text, one of 3 articles or fewer, are
//     hidden the same way and left out of the sitemaps (shortTextHidden):
//     the text's own page already shows every article whole, so each such
//     page repeated it (92,053 of 289,082 article URLs in the sitemaps on
//     4 Nov 2026; Nizar, 4 Nov 2026). Long texts' articles stay open.
//   - A page that is noindex, or whose twin in the other language is, names
//     no hreflang (Page.Hreflang): hreflang never points at a hidden page. A
//     hidden English text page stays its own canonical: a noindex page whose
//     canonical names another address sends Google two signals.

// canonical is the page's canonical path (with /en for the English interface;
// Arabic is the root) and whether the en and ar versions are a translation pair.
func (s *Server) canonical(r *http.Request, p Page) (string, bool) {
	if s.isStatic {
		return p.Path, false
	}
	if p.Lang == "ar" && !p.Translated {
		return p.Path, false
	}
	return s.u(r, p.Path), true
}

// noindexed reports whether the build listed a page as having nothing to
// index: kind is article (id the text, pos the article's position),
// ruling, opinion, agreement, doctrine or research (pos 0), or english (id
// the text, pos 0: all of its /en/ pages). A database from before the table
// has none.
func (s *Server) noindexed(ctx context.Context, kind string, id, pos int) bool {
	var one int
	return s.db.QueryRow(ctx, `SELECT 1 FROM noindex_page WHERE kind = $1 AND id = $2 AND position = $3`, kind, id, pos).Scan(&one) == nil
}

// rulingsHidden: every court ruling page and every list of rulings (/rulings/,
// /rulings/years/, /rulings/year/N/, an article's /rulings/ list), in both
// languages, is noindex, follow and in no sitemap. Rulings name people
// (parties, lawyers, judges); CanLII and AustLII keep theirs out of search
// engines too (Nizar, 2 Nov 2026, issue #86). The pages stay for visitors
// and the laws keep linking to them.
const rulingsHidden = true

// shortTextArticles is the most articles a text may have for its article
// pages to be hidden as copies of its own page.
const shortTextArticles = 3

// shortTextHidden reports whether the article pages of a text with this many
// articles are hidden: the text's page holds them all.
func shortTextHidden(articles int) bool { return articles <= shortTextArticles }

// englishHidden reports whether a text's machine-translated /en/ pages are
// hidden from search engines (it is not a popular text).
func (s *Server) englishHidden(ctx context.Context, id int) bool {
	return s.db != nil && s.noindexed(ctx, "english", id, 0)
}

// weakText marks a text's page (or one of its articles) for its English
// twin: noindex on the /en/ address of a text that is not popular, and no
// hreflang on either address.
func (s *Server) weakText(ctx context.Context, r *http.Request, p *Page, id int) {
	if !s.englishHidden(ctx, id) {
		return
	}
	p.TwinHidden = true
	if s.lang(r) != "ar" {
		p.NoIndex = true
	}
}

// ---------------------------------------------------------------- pagination

// A list page is /opinions/ then /opinions/page/2/, and a year's list is
// /laws/year/2020/ then /laws/year/2020/page/2/: paths a crawler follows
// and indexes, where "?page=2" is blocked by robots.txt (Disallow: /*?).
// Lists with a filter keep "?page=" and stay noindex.

// yearPageSize is how many texts a year's list shows: about 150 KB and 4,000
// DOM nodes a page, where the whole of a year had 1.5 MB and 31,000.
const yearPageSize = 300

// pagePath is page n of the list at base ("/opinions/").
func pagePath(base string, n int) string {
	if n <= 1 {
		return base
	}
	return base + "page/" + strconv.Itoa(n) + "/"
}

// pageNums are the page numbers a pager links to, with 0 where numbers are
// skipped: the first and last, a window round the current page and, in a long
// list, every tenth page (every hundredth when there are hundreds), so any
// page of a list of up to 300 is two clicks from the first and a crawler never
// walks a list one page at a time.
func pageNums(page, pages int) []int {
	if pages <= 1 {
		return nil
	}
	want := map[int]bool{1: true, pages: true}
	reach := 2
	if pages > 12 {
		reach = 5 // from any tenth page, every page up to the next is one click away
	}
	for i := page - reach; i <= page+reach; i++ {
		want[i] = true
	}
	if pages > 12 {
		step := 10
		if pages > 300 {
			step = 100
		}
		for i := step; i < pages; i += step {
			want[i] = true
		}
		if pages > 300 { // and tens round the current page
			for i := (page - 50) / 10 * 10; i <= page+50 && i < pages; i += 10 {
				want[i] = true
			}
		}
	} else {
		for i := 1; i <= pages; i++ {
			want[i] = true
		}
	}
	var ns []int
	for n := range want {
		if n >= 1 && n <= pages {
			ns = append(ns, n)
		}
	}
	sort.Ints(ns)
	var out []int
	for i, n := range ns {
		if i > 0 && n != ns[i-1]+1 {
			out = append(out, 0)
		}
		out = append(out, n)
	}
	return out
}

// pageArg reads which page of a list is asked for, from /list/page/N/ or the
// older ?page=N. done is true when the response has been written: the older
// address is redirected (301) to the path, page 1 to the list itself, a page
// past the end is a 404. An address with a filter keeps ?page= and is left to
// the caller (noindex).
func (s *Server) pageArg(w http.ResponseWriter, r *http.Request, base string, q url.Values, pages int) (page int, done bool) {
	pages = max(pages, 1)
	if v := r.PathValue("page"); v != "" {
		n, err := strconv.Atoi(v)
		switch {
		case err != nil || n < 1 || n > pages:
			s.notFound(w, r)
			return 0, true
		case n == 1:
			http.Redirect(w, r, s.u(r, base), http.StatusMovedPermanently)
			return 0, true
		}
		return n, false
	}
	if _, has := q["page"]; has && len(q) == 1 && !s.isStatic {
		n, _ := strconv.Atoi(q.Get("page"))
		http.Redirect(w, r, s.u(r, pagePath(base, min(max(n, 1), pages))), http.StatusMovedPermanently)
		return 0, true
	}
	return min(pageNo(q), pages), false
}

// pageSuffix marks a later page's title and description.
func (s *Server) pageSuffix(r *http.Request, page int) string {
	if page <= 1 {
		return ""
	}
	return " · " + fmt.Sprint(s.tr(r, "pager.page_n", thousands(page)))
}

// listPaging settles which page of a list is asked for. pages follows from
// total and size. listBase is the list's path when its pages are paths (no
// filter in the address), empty when they are ?page= (a filtered list).
func (s *Server) listPaging(w http.ResponseWriter, r *http.Request, base string, q url.Values, total, size int) (page, pages int, listBase string, done bool) {
	pages = max(1, (total+size-1)/size)
	if r.PathValue("page") != "" && len(q) > 0 {
		s.notFound(w, r) // a filter on a path page: there is no such address
		return 0, pages, "", true
	}
	page, done = s.pageArg(w, r, base, q, pages)
	if done {
		return 0, pages, "", true
	}
	if len(q) == 0 && !s.isStatic {
		listBase = base
	}
	return page, pages, listBase, false
}
