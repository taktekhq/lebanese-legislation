package site

import "net/http"

func (s *Server) contribute(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "contribute", http.StatusOK, Page{
		Title: s.brandTitle(r, s.tr(r, "contribute.title")),
		Desc:  s.tr(r, "contribute.desc"), Path: "/contribute/",
		// Drawn in docs/og/contribute.html, rendered by scripts/og-image.cjs.
		OGImage: "/assets/og-contribute-" + s.lang(r) + ".png", OGImageAlt: s.tr(r, "contribute.og_alt"),
		NoIndex: r.URL.RawQuery != "",
	})
}
