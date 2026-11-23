package site

import "net/http"

func (s *Server) contribute(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "contribute", http.StatusOK, Page{
		Title: s.brandTitle(r, s.tr(r, "contribute.title")),
		Desc:  s.tr(r, "contribute.desc"), Path: "/contribute/",
		NoIndex: r.URL.RawQuery != "",
	})
}
