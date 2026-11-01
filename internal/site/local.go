package site

import "net/http"

// localPage is "Run it yourself": the Docker Compose setup for the website,
// API and MCP server on your own machine.
func (s *Server) localPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "local", http.StatusOK, Page{
		Title: s.brandTitle(r, s.tr(r, "local.title")),
		Desc:  s.tr(r, "local.desc"), Path: "/run-it-yourself/",
		NoIndex: r.URL.RawQuery != "",
	})
}
