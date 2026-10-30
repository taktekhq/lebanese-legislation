package site

import (
	"fmt"
	"net/http"
	"strings"
)

var aiCrawlers = []string{"GPTBot", "OAI-SearchBot", "ChatGPT-User", "ClaudeBot", "Claude-SearchBot",
	"Claude-User", "PerplexityBot", "Perplexity-User", "Google-Extended", "Applebot-Extended", "CCBot"}

func (s *Server) seoRoutes() {
	m := s.mux
	m.HandleFunc("GET /robots.txt", s.robots)
	m.HandleFunc("GET /llms.txt", s.llms)
	m.HandleFunc("GET /sitemap.xml", s.sitemapIndex)
	m.HandleFunc("GET /sitemaps/{name}", s.sitemap)
}

func (s *Server) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	w.Header().Set("Cache-Control", "public, max-age=600")
	rules := "Allow: /\nDisallow: /search/\nDisallow: /en/search/\nDisallow: /api/\nDisallow: /changes/review/\nDisallow: /en/changes/review/\nDisallow: /*?\n"
	var b strings.Builder
	b.WriteString("# Lebanese Legislation: every Lebanese law, free to read, cite and build on.\n")
	b.WriteString("# Search engines and AI assistants are welcome; please cite the article's address.\n\n")
	b.WriteString("User-agent: *\n" + rules + "\n")
	for _, bot := range aiCrawlers {
		b.WriteString("User-agent: " + bot + "\n" + rules + "\n")
	}
	b.WriteString("Sitemap: " + Base + "/sitemap.xml\n")
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
		{"Doctrine", "/doctrine/"}, {"Latest changes (amendments, newest first)", "/changes/"}, {"The database (PostgreSQL)", "/data/"}, {"MCP server and JSON API", "/ai/"}} {
		fmt.Fprintf(&b, "- [%s](%s%s)\n", l[0], Base, l[1])
	}
	b.WriteString("\n## For AI assistants\n\n- MCP server (search, get_law, get_article, get_ruling, get_gazette_issue; verbatim texts only): " + Base + "/mcp\n- Read-only JSON API: " + Base + "/ai/\n")
	b.WriteString("\n## Notes\n\n- Texts come from the Lebanese University's Centre for Legal Informatics; the Official Gazette is authoritative.\n- Nothing here is legal advice.\n")
	fmt.Fprint(w, b.String())
}
