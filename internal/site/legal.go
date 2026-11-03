package site

import "net/http"

// The privacy policy and the terms of use: short pages in the footer. The
// privacy text is the one the About page carried (about.privacy_*), so the
// two never say different things.

func (s *Server) privacy(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "privacy", http.StatusOK, Page{
		Title: s.brandTitle(r, s.tr(r, "privacy.title")),
		Desc:  s.tr(r, "privacy.desc"), Path: "/privacy/",
		NoIndex: r.URL.RawQuery != "",
	})
}

func (s *Server) terms(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "terms", http.StatusOK, Page{
		Title: s.brandTitle(r, s.tr(r, "terms.title")),
		Desc:  s.tr(r, "terms.desc"), Path: "/terms/",
		NoIndex: r.URL.RawQuery != "",
	})
}
