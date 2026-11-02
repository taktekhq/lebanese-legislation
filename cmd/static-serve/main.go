// static-serve serves the static exporter keys without a database.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var securityHeaders = map[string]string{
	"Strict-Transport-Security":    "max-age=31536000; includeSubDomains",
	"X-Content-Type-Options":       "nosniff",
	"X-Frame-Options":              "DENY",
	"Referrer-Policy":              "strict-origin-when-cross-origin",
	"Permissions-Policy":           "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()",
	"Cross-Origin-Opener-Policy":   "same-origin",
	"Cross-Origin-Resource-Policy": "same-site",
	"Content-Security-Policy":      "default-src 'self'; worker-src 'self' blob:; script-src 'self' 'sha256-WZRJfWvsnNCPcxzZwvyhovnZGqhZaC+8gPGPRbx6wTk=' https://www.googletagmanager.com https://static.cloudflareinsights.com; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data: https://*.google-analytics.com https://*.googletagmanager.com https://analytics.google.com https://*.analytics.google.com https://*.g.doubleclick.net https://www.google.com; connect-src 'self' https://*.google-analytics.com https://analytics.google.com https://*.analytics.google.com https://*.g.doubleclick.net https://www.google.com https://*.googletagmanager.com https://cloudflareinsights.com; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'",
}
var escapes = regexp.MustCompile(`%[0-9a-fA-F]{2}`)
var tracking = regexp.MustCompile(`(?i)^(utm_[a-z0-9_]+|gclid|dclid|fbclid|msclkid|yclid|igshid|mc_cid|mc_eid|ref_src)$`)

func keyFor(p string) string {
	p = escapes.ReplaceAllStringFunc(p, strings.ToUpper)
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return ""
		}
	}
	key := strings.TrimPrefix(p, "/")
	if p == "/" || strings.HasSuffix(p, "/") {
		key += "index.html"
	}
	if strings.HasPrefix(key, "_export/") || strings.HasPrefix(key, "_build/") {
		return ""
	}
	return key
}
func dynamic(r *http.Request) bool {
	if r.Method != "GET" && r.Method != "HEAD" {
		return true
	}
	p := r.URL.Path
	if p == "/en" {
		p = "/"
	} else {
		p = strings.TrimPrefix(p, "/en/")
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
	}
	for _, prefix := range []string{"/api/", "/ph/", "/ph", "/mcp", "/healthz", "/.well-known/", "/ul/", "/scan/", "/ruling-files/", "/official", "/official-files/"} {
		if p == prefix || strings.HasPrefix(p, strings.TrimSuffix(prefix, "/")+"/") || (prefix == "/official" && strings.HasPrefix(p, prefix)) {
			return true
		}
	}
	for k := range r.URL.Query() {
		if !tracking.MatchString(k) {
			return true
		}
	}
	return false
}

type site struct {
	root      *os.Root
	redirects RedirectData
}

func (s *site) open(p string) (*os.File, error) {
	key := keyFor(p)
	if key == "" {
		return nil, os.ErrNotExist
	}
	f, err := s.root.Open(key)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}
func (s *site) exists(p string) bool {
	f, err := s.open(p)
	if err != nil {
		return false
	}
	f.Close()
	return true
}
func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for k, v := range securityHeaders {
		w.Header().Set(k, v)
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	p := r.URL.EscapedPath()
	if !dynamic(r) {
		if f, err := s.open(p); err == nil {
			defer f.Close()
			st, _ := f.Stat()
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.Header().Set("X-Served-By", "static")
			http.ServeContent(w, r, filepath.Base(keyFor(p)), st.ModTime(), f)
			return
		}
		if keyFor(p) != "" {
			if to := s.redirects.Resolve(p, s.exists); to != p {
				if r.URL.RawQuery != "" {
					to += "?" + r.URL.RawQuery
				}
				w.Header().Set("Location", to)
				w.Header().Set("Cache-Control", "public, max-age=3600")
				w.WriteHeader(301)
				return
			}
		}
	}
	f, err := s.root.Open("404.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", mime.TypeByExtension(".html"))
	w.WriteHeader(404)
	if r.Method != "HEAD" {
		f.WriteTo(w)
	}
}
func main() {
	dir := flag.String("root", "/site", "exported R2 keys")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()
	root, err := os.OpenRoot(*dir)
	if err != nil {
		log.Fatal(err)
	}
	defer root.Close()
	f, err := root.Open("_export/redirects.json")
	if err != nil {
		log.Fatal(err)
	}
	s := &site{root: root}
	err = json.NewDecoder(f).Decode(&s.redirects)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}
	s.redirects.index()
	log.Fatal((&http.Server{Addr: *addr, Handler: s, ReadHeaderTimeout: 10 * time.Second}).ListenAndServe())
}
