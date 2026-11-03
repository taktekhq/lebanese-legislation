package mcpserver

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Live stat badges (shields.io style SVGs) for pages we publish but can't
// put gtag on, such as the Hugging Face dataset card (Markdown, no
// scripts). Each badge shows a current number from the corpus, so the card
// never goes stale between uploads.
//
// One badge per page also counts the view: the card adds ?src=hf to
// exactly one of its badges, and that request records a GA4 page_view in
// the page's own stream. Hugging Face doesn't proxy external images, so
// each visitor's browser fetches the badge itself. Badges without src
// (e.g. in a GitHub README, which GitHub proxies through its image cache
// and would miscount) only show the number.

type badgePage struct {
	location string
	title    string
	url      string // the page's real address, for PostHog
}

// badgePages are the pages whose views a badge may count, by ?src= value.
// location must be on the GA stream's own domain: GA dropped page_views
// whose page_location was huggingface.co.
var badgePages = map[string]badgePage{
	"hf": {
		location: "https://lebaneselegislation.com/huggingface",
		title:    "Lebanese Legislation dataset (Hugging Face)",
		url:      "https://huggingface.co/datasets/taktekhq/lebanese-legislation",
	},
	// GitHub serves README images through its camo proxy, which honours
	// no-store, so each README view fetches the badge once. The count is
	// views, not people: every fetch comes from camo's addresses.
	"gh": {
		location: "https://lebaneselegislation.com/github",
		title:    "Lebanese Legislation repository (GitHub)",
		url:      "https://github.com/taktekhq/lebanese-legislation",
	},
}

type badgeStats struct{ legislation, articles, rulings int }

// The counts read every row of three big tables, so they are cached; they
// only change with the nightly build.
const statsTTL = 6 * time.Hour

func (s *Server) loadStats(ctx context.Context) (badgeStats, error) {
	s.badgeMu.Lock()
	defer s.badgeMu.Unlock()
	if !s.badgeAt.IsZero() && time.Since(s.badgeAt) < statsTTL {
		return s.badgeStats, nil
	}
	var st badgeStats
	err := s.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM `+lawFrom+`)::int,
		(SELECT count(*) FROM legislation_article)::int, (SELECT count(*) FROM ruling)::int`).Scan(&st.legislation, &st.articles, &st.rulings)
	if err != nil {
		return st, err
	}
	s.badgeStats, s.badgeAt = st, time.Now()
	return st, nil
}

var thousandsRe = regexp.MustCompile(`(\d)(\d{3})($|,)`)

// thousands writes 1234567 as "1,234,567".
func thousands(n int) string {
	s := strconv.Itoa(n)
	for {
		t := thousandsRe.ReplaceAllString(s, "$1,$2$3")
		if t == s {
			return s
		}
		s = t
	}
}

type badgeDef struct {
	label      string
	value      func(badgeStats) string
	needsStats bool
	load       func(*Server, context.Context) (string, error) // a value read from elsewhere (Hugging Face)
}

var badges = map[string]badgeDef{
	"legislation": {label: "legislation", value: func(s badgeStats) string { return thousands(s.legislation) + " texts" }, needsStats: true},
	"articles":    {label: "articles", value: func(s badgeStats) string { return thousands(s.articles) }, needsStats: true},
	"rulings":     {label: "court rulings", value: func(s badgeStats) string { return thousands(s.rulings) }, needsStats: true},
	"mcp":         {label: "MCP server", value: func(badgeStats) string { return "online" }},
	"updated": {label: "last updated", value: func(badgeStats) string { return "" }, load: func(s *Server, ctx context.Context) (string, error) {
		iso, err := s.hfLastModified(ctx)
		if err != nil {
			return "", err
		}
		return badgeDay(iso), nil
	}},
	"downloads": {label: "Hugging Face downloads", value: func(badgeStats) string { return "" }, load: func(s *Server, ctx context.Context) (string, error) {
		n, err := s.hfDownloads(ctx)
		if err != nil {
			return "", err
		}
		return thousands(n), nil
	}},
}

var months = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// badgeDay writes an ISO timestamp as "20 Oct 2026" (UTC).
func badgeDay(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return "Invalid Date"
	}
	t = t.UTC()
	return fmt.Sprintf("%d %s %d", t.Day(), months[t.Month()-1], t.Year())
}

const (
	labelColor = "#555"
	valueColor = "#2b5c97" // the site's --accent
	mutedColor = "#9f9f9f" // when the number couldn't be read
)

// textWidth approximates Verdana 11px, which is what the SVG asks for;
// close enough that the text never overflows its box.
func textWidth(s string) int {
	w := 0.0
	for _, ch := range s {
		switch {
		case strings.ContainsRune("il.,:;|!'1 ", ch):
			w += 3.9
		case strings.ContainsRune("mwMW", ch):
			w += 10
		case ch >= '0' && ch <= '9':
			w += 7
		case ch >= 'A' && ch <= 'Z':
			w += 7.6
		default:
			w += 6.4
		}
	}
	return int(math.Ceil(w))
}

var svgEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// jsHalf writes n/2 as JavaScript prints it ("40" or "40.5").
func jsHalf(n int) string { return jsNumberString(float64(n) / 2) }

func renderBadge(label, value, color string) string {
	lw := textWidth(label) + 12
	vw := textWidth(value) + 12
	w := lw + vw
	l, v := svgEsc.Replace(label), svgEsc.Replace(value)
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">`, w, l, v) +
		fmt.Sprintf(`<title>%s: %s</title>`, l, v) +
		`<linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>` +
		fmt.Sprintf(`<clipPath id="r"><rect width="%d" height="20" rx="3" fill="#fff"/></clipPath>`, w) +
		fmt.Sprintf(`<g clip-path="url(#r)"><rect width="%d" height="20" fill="%s"/><rect x="%d" width="%d" height="20" fill="%s"/><rect width="%d" height="20" fill="url(#s)"/></g>`, lw, labelColor, lw, vw, color, w) +
		`<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">` +
		fmt.Sprintf(`<text x="%s" y="15" fill="#010101" fill-opacity=".3">%s</text><text x="%s" y="14">%s</text>`, jsHalf(lw), l, jsHalf(lw), l) +
		fmt.Sprintf(`<text x="%s" y="15" fill="#010101" fill-opacity=".3">%s</text><text x="%s" y="14">%s</text>`, jsNumberString(float64(lw)+float64(vw)/2), v, jsNumberString(float64(lw)+float64(vw)/2), v) +
		`</g></svg>`
}

// Crawlers and link previewers that fetch images are not readers.
var botUA = regexp.MustCompile(`(?i)bot|crawl|spider|slurp|preview|fetch|curl|wget|python|httpx|go-http|java/|headless`)

func isBot(ua string) bool { return ua == "" || botUA.MatchString(ua) }

func svgResponse(w http.ResponseWriter, r *http.Request, svg string, status int) {
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml; charset=utf-8")
	// Every view must reach the server (for the count and the live
	// number), not a browser or edge cache.
	h.Set("Cache-Control", "no-store, max-age=0")
	h.Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(svg))
	}
}

var badgePath = regexp.MustCompile(`^/badge/([a-z]+)\.svg$`)

// serveBadge serves /badge/<name>.svg[?src=<page>].
func (s *Server) serveBadge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	m := badgePath.FindStringSubmatch(r.URL.Path)
	var def badgeDef
	found := false
	if m != nil {
		def, found = badges[m[1]]
	}
	if !found {
		svgResponse(w, r, renderBadge("badge", "not found", mutedColor), http.StatusNotFound)
		return
	}
	name := m[1]
	if page, has := badgePages[r.URL.Query().Get("src")]; has && r.Method == http.MethodGet && !isBot(r.Header.Get("User-Agent")) {
		ref := r.Header.Get("Referer")
		s.track(s.hfStream(), r, []gaEvent{{name: "page_view", params: obj{{"page_location", page.location}, {"page_title", page.title}, {"page_referrer", ref}, {"badge", name}}}})
		pu, _ := url.Parse(page.url)
		props := obj{{"$current_url", page.url}, {"$host", pu.Host}, {"$pathname", pu.Path}, {"$title", page.title}}
		if _, has := r.Header["Referer"]; has {
			props.set("$referrer", ref)
		}
		props.set("$user_agent", r.Header.Get("User-Agent"))
		props.set("badge", name)
		s.phCapture(r, []phEvent{{event: "$pageview", properties: props}})
	}
	ctx := r.Context()
	if def.load != nil {
		v, err := def.load(s, ctx)
		if err != nil {
			s.logf("badge %s: %v", name, err)
			svgResponse(w, r, renderBadge(def.label, "n/a", mutedColor), http.StatusOK)
			return
		}
		svgResponse(w, r, renderBadge(def.label, v, valueColor), http.StatusOK)
		return
	}
	if !def.needsStats {
		svgResponse(w, r, renderBadge(def.label, def.value(badgeStats{}), valueColor), http.StatusOK)
		return
	}
	st, err := s.loadStats(ctx)
	if err != nil {
		s.logf("badge stats: %v", err)
		svgResponse(w, r, renderBadge(def.label, "n/a", mutedColor), http.StatusOK)
		return
	}
	svgResponse(w, r, renderBadge(def.label, def.value(st), valueColor), http.StatusOK)
}
