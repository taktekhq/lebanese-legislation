package site

import (
	"log"
	"net/http"
)

func (s *Server) captureError(r *http.Request, kind string, err any) {
	log.Printf("%s %s: %v", kind, r.URL.Path, err)
}
