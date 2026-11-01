// SPDX-License-Identifier: MIT

// Package linkdepth measures how many clicks a crawler needs to reach a
// sample of a site's pages, following only plain <a href> links.
//
// It answers one question: of the pages a sitemap lists, how many can be
// found by following links from the home page, and how deep are they. A page
// that only the sitemap lists is a page Google has to be told about; a page
// five clicks down is crawled last.
//
// The crawl is bounded. It expands every "hub" page (lists, years, topics,
// pagination), because those are few and carry the links, and it expands a
// text page only when it is the parent of a sampled page (a law page, its
// parts), because 400,000 article pages cannot be fetched politely. So a
// reported depth is the depth by hub navigation plus the sampled page's own
// parent, which is what a crawler that starts from the home page sees first.
package linkdepth

import (
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// Links returns the distinct same-host paths an HTML page links to with <a
// href>. Links with a query string are dropped (robots.txt disallows them and
// a crawler does not follow them), as are fragments, other hosts, mailto:
// and the like. Paths are returned as served, with their trailing slash.
func Links(page *url.URL, body io.Reader) []string {
	seen := map[string]bool{}
	var out []string
	z := html.NewTokenizer(body)
	for {
		switch z.Next() {
		case html.ErrorToken:
			sort.Strings(out)
			return out
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			if t.Data != "a" {
				continue
			}
			for _, a := range t.Attr {
				if a.Key != "href" {
					continue
				}
				if p, ok := Local(page, a.Val); ok && !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
			}
		}
	}
}

// Local resolves href against page and returns its path when it is a link a
// crawler would follow on the same host.
func Local(page *url.URL, href string) (string, bool) {
	u, err := page.Parse(strings.TrimSpace(href))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host != page.Host || u.RawQuery != "" || u.ForceQuery {
		return "", false
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	return p, true
}

var (
	// DefaultHub matches the pages of lebaneselegislation.com that list other
	// pages: the home page, the lists and their year and page variants, the
	// topics, the codes. Override it with a regexp for another site.
	DefaultHub = regexp.MustCompile(`^(/en)?(/|/(laws|rulings|gazette|opinions|agreements|doctrine|research)/(years/|year/\d+/((law|decree|text)/)?(page/\d+/)?|page/\d+/)?|/(codes|topics|changes|blog)/|/codes/[^/]+/(page/\d+/)?|/topic/[^/]+/|/code/[^/]+/.*)$`)
	// the part of an address that names one text: /law/244226/, /en/decree/7/
	textRe = regexp.MustCompile(`^(/en)?/(law|decree|text|ruling|gazette|opinion|agreement|doctrine|research|official)/(\d+)/`)
	// feeds, files and review queues are not pages to browse
	skipRe = regexp.MustCompile(`(\.xml|\.json|\.txt|\.pdf|\.css|\.js|\.png|\.svg|\.woff2|\.ico)$|/changes/review/|^/(assets|ul|scan|ruling-files|official-files)/`)
)

// IsHub reports whether a path is a page that exists to list other pages,
// by hub (nil: DefaultHub).
func IsHub(p string, hub *regexp.Regexp) bool {
	if hub == nil {
		hub = DefaultHub
	}
	return !skipRe.MatchString(p) && hub.MatchString(p)
}

// TextPrefix is the address of the text a path belongs to ("/law/244226/"),
// or "" when the path is not under a text.
func TextPrefix(p string) string {
	m := textRe.FindString(p)
	return m
}

// IsArticle reports whether a path is a single article page.
func IsArticle(p string) bool { return strings.Contains(p, "/article/") }

// Family names the kind of page a path is, for the report: language, then
// kind ("ar article", "en law", "ar ruling").
func Family(p string) string {
	lang := "ar"
	if strings.HasPrefix(p, "/en/") {
		lang, p = "en", strings.TrimPrefix(p, "/en")
	}
	seg := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case len(seg) >= 3 && seg[2] == "article" && (seg[0] == "law" || seg[0] == "decree" || seg[0] == "text"):
		return lang + " article"
	case len(seg) >= 1 && (seg[0] == "law" || seg[0] == "decree" || seg[0] == "text"):
		return lang + " law"
	case len(seg) >= 1 && seg[0] == "":
		return lang + " other"
	}
	return lang + " " + seg[0]
}
