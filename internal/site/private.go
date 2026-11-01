package site

import (
	"crypto/subtle"
	"net/http"
)

// Private puts the site behind one shared password and keeps it out of
// search engines: for previews before launch.
func Private(h http.Handler, user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Lebanese Legislation preview", charset="UTF-8"`)
			http.Error(w, "This preview is private.", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// NoIndex keeps the site out of search engines without a password: for a
// public preview before launch. robots.txt disallows crawling and every
// response says noindex.
func NoIndex(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=600")
			w.Write([]byte("User-agent: *\nDisallow: /\n"))
			return
		}
		h.ServeHTTP(w, r)
	})
}
