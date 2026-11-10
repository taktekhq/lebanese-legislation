package site

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
)

// Arabic is the site's default: its pages live at the root (/law/12/). The
// English interface lives under /en/. The French interface lives under /fr/
// and covers the site's own pages only (frenchPaths): any other address under
// /fr/ is sent to its /en/ twin, and French pages link texts, articles and
// rulings to their existing /en/ pages. The old /ar/... addresses answer a
// GET or HEAD with a 301 to the same path at the root (query kept), or to
// where that path has moved since (redirect.go), in one hop; other
// methods are served as Arabic
// without a redirect. withLang strips the /en or /ar prefix before the
// request reaches the mux, remembers the language in the request's context,
// and patches up any redirect the inner handler issues so an /en request
// stays under /en.

type ctxKey int

const langCtxKey ctxKey = 1

// langPrefixes are the address prefixes that select a language; the root
// (no prefix) is Arabic.
var langPrefixes = map[string]string{"/en": "en", "/ar": "ar", "/fr": "fr"}

// frenchPaths are the pages that have a French version, as root paths
// (i18n.FrenchPages).
var frenchPaths = i18n.FrenchPages

// FrenchPage reports whether the page at a root path has a French version.
func FrenchPage(path string) bool { return frenchPaths[path] }

// FrenchPaths lists the root paths of the pages that have a French version, sorted.
func FrenchPaths() []string {
	out := make([]string, 0, len(frenchPaths))
	for p := range frenchPaths {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// frLink is where a French page links an internal path: its French version
// when there is one, else the English one (a query or fragment is kept).
func frLink(p string) string {
	bare := p
	if i := strings.IndexAny(bare, "?#"); i >= 0 {
		bare = bare[:i]
	}
	if frenchPaths[bare] {
		return "/fr" + p
	}
	return "/en" + p
}

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
			if lang == "fr" && !frenchPaths[strings.TrimPrefix(p, prefix)] {
				// no French version of this page: its English one, query kept.
				// Temporary, so a page that gains a French version is found.
				to := "/en" + strings.TrimPrefix(r.URL.EscapedPath(), prefix)
				if r.URL.RawQuery != "" {
					to += "?" + r.URL.RawQuery
				}
				w.Header().Set("Cache-Control", "public, max-age=300")
				http.Redirect(w, r, to, http.StatusFound)
				return
			}
			r2 := r.Clone(context.WithValue(r.Context(), langCtxKey, lang))
			r2.URL.Path = strings.TrimPrefix(p, prefix)
			// the raw path loses the same prefix; when it spells it another
			// way ("/en%2F..."), it is dropped and rebuilt from the path
			r2.URL.RawPath = ""
			if raw := r.URL.RawPath; strings.HasPrefix(raw, prefix+"/") {
				r2.URL.RawPath = strings.TrimPrefix(raw, prefix)
			}
			if lang == "ar" { // a POST under /ar/: redirects it issues go to the root pages
				next.ServeHTTP(w, r2)
				return
			}
			if lang == "fr" { // a redirect to a page with no French version goes to its English one
				next.ServeHTTP(&redirectFixer{ResponseWriter: w, prefix: prefix, to: frLink}, r2)
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
	to     func(path string) string // where a root path goes, when not simply under prefix
}

func (f *redirectFixer) WriteHeader(code int) {
	if !f.fixed {
		f.fixed = true
		if loc := f.Header().Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, f.prefix+"/") && loc != f.prefix {
			if f.to != nil {
				loc = f.to(loc)
			} else {
				loc = f.prefix + loc
			}
			f.Header().Set("Location", loc)
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

// lang is the UI language of this request: "en", "ar" or "fr".
func (s *Server) lang(r *http.Request) string { return langFromContext(r.Context()) }

// tr looks up a UI string in the request's language, for the Go-side
// titles and descriptions built in handlers.go, docs.go, versions.go.
func (s *Server) tr(r *http.Request, key string, args ...any) string {
	return s.bundle(r).T(key, args...)
}

// trOr returns en, unchanged, on the English site, and the Arabic
// translation of key on the Arabic one. Several Go-built strings already
// mix English and Arabic (e.g. "Court rulings · الأحكام"), so the
// English site keeps exactly its current wording while the Arabic site switches
// fully to ar.json instead of layering a second Arabic phrase onto one
// that already has one.
func (s *Server) trOr(r *http.Request, en, key string, args ...any) string {
	if l := s.lang(r); l == "ar" || l == "fr" {
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
	if s.isStatic {
		return path
	}
	switch s.lang(r) {
	case "en":
		return "/en" + path
	case "fr":
		return frLink(path)
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

// stripLang is a path without its /en, /fr or /ar prefix.
func stripLang(p string) string {
	for prefix := range langPrefixes {
		if strings.HasPrefix(p, prefix+"/") {
			return strings.TrimPrefix(p, prefix)
		}
	}
	return p
}
