package site

import (
	"context"
	"net/http"
	"strings"

	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
)

// Arabic is the site's default: its pages live at the root (/law/12/). The
// English interface lives under /en/. The old /ar/... addresses answer a
// GET or HEAD with a 301 to the same path at the root (query kept), or to
// where that path has moved since (redirect.go), in one hop; other
// methods (the notify form posts under the page prefix) are served as Arabic
// without a redirect. withLang strips the /en or /ar prefix before the
// request reaches the mux, remembers the language in the request's context,
// and patches up any redirect the inner handler issues so an /en request
// stays under /en.

type ctxKey int

const langCtxKey ctxKey = 1

// langPrefixes are the address prefixes that select a language; the root
// (no prefix) is Arabic.
var langPrefixes = map[string]string{"/en": "en", "/ar": "ar"}

func (s *Server) withLang(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		for prefix, lang := range langPrefixes {
			if p == prefix {
				if lang == "ar" {
					s.redirectFinal(w, r, r.URL.EscapedPath())
				} else {
					http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
				}
				return
			}
			if !strings.HasPrefix(p, prefix+"/") {
				continue
			}
			if lang == "ar" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
				s.redirectFinal(w, r, r.URL.EscapedPath())
				return
			}
			r2 := r.Clone(context.WithValue(r.Context(), langCtxKey, lang))
			r2.URL.Path = strings.TrimPrefix(p, prefix)
			if raw := r.URL.RawPath; raw != "" {
				r2.URL.RawPath = strings.TrimPrefix(raw, prefix)
			}
			if lang == "ar" { // a POST under /ar/: redirects it issues go to the root pages
				next.ServeHTTP(w, r2)
				return
			}
			next.ServeHTTP(&redirectFixer{ResponseWriter: w, prefix: prefix}, r2)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), langCtxKey, "ar")))
	})
}

// redirectFixer prepends the language prefix back onto a same-site redirect
// Location an inner handler sets (the trailing-slash redirect, /code/{slug},
// the go-to-article box), so a request under /en is redirected to another
// /en page, never dropped back to the Arabic root.
type redirectFixer struct {
	http.ResponseWriter
	prefix string
	fixed  bool
}

func (f *redirectFixer) WriteHeader(code int) {
	if !f.fixed {
		f.fixed = true
		if loc := f.Header().Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, f.prefix+"/") && loc != f.prefix {
			f.Header().Set("Location", f.prefix+loc)
		}
	}
	f.ResponseWriter.WriteHeader(code)
}

func langFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(langCtxKey).(string); ok {
		return v
	}
	return "en" // a static copy, which has no prefixes
}

// lang is the UI language of this request: "en" or "ar".
func (s *Server) lang(r *http.Request) string { return langFromContext(r.Context()) }

// tr looks up a UI string in the request's language, for the Go-side
// titles and descriptions built in handlers.go, docs.go, versions.go.
func (s *Server) tr(r *http.Request, key string, args ...any) string {
	return i18n.New(s.lang(r)).T(key, args...)
}

// trOr returns en, unchanged, on the English site, and the Arabic
// translation of key on the Arabic one. Several Go-built strings already
// mix English and Arabic (e.g. "Court rulings · الأحكام"), so the
// English site keeps exactly its current wording while the Arabic site switches
// fully to ar.json instead of layering a second Arabic phrase onto one
// that already has one.
func (s *Server) trOr(r *http.Request, en, key string, args ...any) string {
	if s.lang(r) == "ar" {
		return s.tr(r, key, args...)
	}
	return en
}

// brandTitle appends " · <brand>" the way every page title on the site
// does, translating the brand name on the Arabic site too.
func (s *Server) brandTitle(r *http.Request, main string) string {
	return main + " · " + s.tr(r, "brand.name")
}

// u prefixes an internal path with /en for the English interface (Arabic is
// the root), for the handful of links built in Go (handlers.go, docs.go)
// rather than templates. A static copy has no prefixes.
func (s *Server) u(r *http.Request, path string) string {
	if s.lang(r) == "en" && !s.isStatic {
		return "/en" + path
	}
	return path
}

// docPath is the canonical path of a page whose main content is Arabic (a
// text, an article, a ruling): the root address, unless the page has English
// of its own (translated), where the English interface names its /en twin.
func (s *Server) docPath(r *http.Request, path string, translated bool) string {
	if translated {
		return s.u(r, path)
	}
	return path
}

// stripLang is a path without its /en or /ar prefix.
func stripLang(p string) string {
	for prefix := range langPrefixes {
		if strings.HasPrefix(p, prefix+"/") {
			return strings.TrimPrefix(p, prefix)
		}
	}
	return p
}
