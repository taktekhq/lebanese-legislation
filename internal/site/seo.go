package site

import (
	"fmt"
	"net/http"
	"strings"
)

// What search engines and AI crawlers read: sitemaps for every document,
// robots.txt, llms.txt and the IndexNow key. The sitemaps are in sitemap.go.

const (
	// IndexNowKey is shared by every Taktek site; the file proves we own the host.
	IndexNowKey = "ea77b6da080977a1becb866c3314f534"
)

// The crawlers that feed answer engines and agents, welcomed by name. Unlike
// "*" (search engines), they may also read search, the API and the MCP server,
// so the assistants and agents that use them learn them (Nizar, 4 Nov 2026).
var aiCrawlers = []string{"GPTBot", "OAI-SearchBot", "ChatGPT-User", "ClaudeBot", "Claude-SearchBot",
	"Claude-User", "PerplexityBot", "Perplexity-User", "Google-Extended", "Applebot-Extended", "CCBot",
	"ShapBot", "DuckAssistBot", "MistralAI-User"}

func (s *Server) seoRoutes() {
	m := s.mux
	m.HandleFunc("GET /robots.txt", s.robots)
	m.HandleFunc("GET /llms.txt", s.llms)
	m.HandleFunc("GET /openapi.json", s.openapi)
	m.HandleFunc("GET /"+IndexNowKey+".txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, IndexNowKey)
	})
	m.HandleFunc("GET /sitemap.xml", s.sitemapIndex)
	m.HandleFunc("GET /sitemaps/{name}", s.sitemap)
}

func (s *Server) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// short, so a change (like the noindex preview ending) reaches crawlers
	// through Cloudflare's cache within minutes, not its default 4 hours
	w.Header().Set("Cache-Control", "public, max-age=600")
	rules := "Allow: /\nDisallow: /search/\nDisallow: /en/search/\nDisallow: /api/\nDisallow: /changes/review/\nDisallow: /en/changes/review/\nDisallow: /*?\n"
	var b strings.Builder
	b.WriteString("# Lebanese Legislation: every Lebanese law, free to read, cite and build on.\n")
	b.WriteString("# Search engines and AI assistants are welcome; please cite the article's address.\n\n")
	aiRules := "Allow: /\nDisallow: /changes/review/\nDisallow: /en/changes/review/\n"
	b.WriteString("User-agent: *\n" + rules + "\n")
	for _, bot := range aiCrawlers {
		b.WriteString("User-agent: " + bot + "\n" + aiRules + "\n")
	}
	b.WriteString("Sitemap: " + Base + "/sitemap.xml\n")
	b.WriteString("Sitemap: " + Base + "/blog-sitemap.xml\n")
	fmt.Fprint(w, b.String())
}

func (s *Server) llms(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	st, err := s.stats(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	codes, err := s.codeRows(ctx, 100)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	var b strings.Builder
	fmt.Fprintf(&b, "# Lebanese Legislation\n\n> Every Lebanese law, decree and ruling, free: %s texts (%s with full text, %s articles), %s court rulings, %s Official Gazette issues, opinions, agreements and doctrine, from the Lebanese University's Centre for Research and Studies in Legal Informatics. Arabic, with the French text where it was published.\n\n",
		thousands(st.Laws), thousands(st.WithText), thousands(st.Articles), thousands(st.Rulings), thousands(st.Gazette))
	b.WriteString("Cite an article by its address, which never changes: " + Base + "/law/{id}/article/{number}/ . Each page shows the text's kind, number, date, Official Gazette issue and whether it is in force.\n\n")
	b.WriteString("The site is in Arabic at the root (" + Base + "/). The English interface is under " + Base + "/en/ (for example " + Base + "/en/law/{id}/); the text of a law is the same Arabic text in both.\n\n")
	b.WriteString("## The codes\n\n")
	for _, c := range codes {
		fmt.Fprintf(&b, "- [%s (%s)](%s%s)\n", c.NameEn, c.Title, Base, lawPath(c.ID))
	}
	b.WriteString("\n## Browse\n\n")
	for _, l := range [][2]string{{"All legislation, by year", "/laws/years/"}, {"Court rulings, by year", "/rulings/years/"},
		{"The Official Gazette, by year", "/gazette/years/"}, {"Legal opinions", "/opinions/"}, {"Agreements", "/agreements/"},
		{"Doctrine", "/doctrine/"}, {"Latest changes (amendments, newest first)", "/changes/"}, {"The database (PostgreSQL)", "/data/"}, {"Lebanese law and AI (no AI answers yet; sign up to hear)", "/ai/"},
		{"Where to read Lebanese law online: every source, free and paid, in one table", "/en/sources/"}, {"Legiliban and Lebanese Legislation: the main source of the texts, and what this site adds", "/en/legiliban/"}} {
		fmt.Fprintf(&b, "- [%s](%s%s)\n", l[0], Base, l[1])
	}
	b.WriteString("\n## For AI assistants\n\n- MCP server (search, get_law, get_article, get_law_texts, get_ruling; verbatim texts only): " + Base + "/mcp\n- Read-only JSON API: " + Base + "/ai/\n- The JSON API as OpenAPI 3.1: " + Base + "/api/v1/openapi.json\n")
	b.WriteString("\n## Notes\n\n- Texts come from Legiliban, the database of the Lebanese University's Centre for Legal Informatics; the Official Gazette is authoritative.\n- This site is independent: it is not affiliated with, endorsed by or operated by the Lebanese University or the Centre.\n- Nothing here is legal advice.\n")
	fmt.Fprint(w, b.String())
}
