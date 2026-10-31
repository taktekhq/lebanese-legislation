package site

import "net/http"

func (s *Server) aiPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "ai", 200, Page{
		Title: "Lebanese law and AI · Lebanese Legislation",
		Desc:  "How AI assistants can read and cite Lebanese law through the API and MCP.",
		Path:  "/ai/", NoIndex: r.URL.RawQuery != "",
		Data: map[string]any{"Static": s.isStatic},
	})
}
