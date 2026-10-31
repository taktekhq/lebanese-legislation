package site

import "net/http"

func (s *Server) ulImage(w http.ResponseWriter, r *http.Request) { s.notFound(w, r) }
func imageURL(s string) string                                   { return s }
