package site

import "net/http"

// aiPage is "Lebanese law and AI": how an AI assistant can read and cite the
// library through the MCP server and the JSON API.
func (s *Server) aiPage(w http.ResponseWriter, r *http.Request) {
	s.track(r, map[string]any{"k": "page", "path": "/ai/"})
	s.render(w, r, "ai", 200, Page{
		Title: "Lebanese law and AI · Lebanese Legislation",
		Desc:  "How AI assistants can read and cite Lebanese law on this site today, through the MCP server and the JSON API.",
		Path:  "/ai/", NoIndex: r.URL.RawQuery != "",
		Data: map[string]any{"Static": s.isStatic},
	})
}
