package site

import (
	"io"
	"net/http"
	"strings"
	"time"
)

// Scanned articles are pictures on the Lebanese University's server, which
// serves them over plain HTTP only (HTTPS answers 500). An HTTPS page cannot
// show them directly, so the site fetches them itself under /ul/uploads/images/
// and marks them cacheable for a year: Cloudflare keeps them, UL is asked once.
const ulImages = "https://legallaw.ul.edu.lb/uploads/images/"

var ulClient = &http.Client{Timeout: 30 * time.Second}

// imageURL maps a stored picture address to the one pages use.
func imageURL(u string) string {
	if strings.HasPrefix(u, ulImages) {
		return "/ul/uploads/images/" + strings.TrimPrefix(u, ulImages)
	}
	return u
}

func (s *Server) ulImage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, "?#\\") {
		http.NotFound(w, r)
		return
	}
	resp, err := ulClient.Get(ulImages + name)
	if err != nil {
		http.Error(w, "The picture could not be fetched from the Lebanese University.", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(ct, "image/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	io.Copy(w, io.LimitReader(resp.Body, 15<<20))
}
