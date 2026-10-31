package site

import (
	"context"
	"net/http"
	"strings"

	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
)

type ctxKey int

const langCtxKey ctxKey = 1

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
			if lang == "ar" {
				next.ServeHTTP(w, r2)
				return
			}
			next.ServeHTTP(&redirectFixer{ResponseWriter: w, prefix: prefix}, r2)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), langCtxKey, "ar")))
	})
}

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
	return "en"
}

func (s *Server) lang(r *http.Request) string { return langFromContext(r.Context()) }

func (s *Server) tr(r *http.Request, key string, args ...any) string {
	return i18n.New(s.lang(r)).T(key, args...)
}

func (s *Server) trOr(r *http.Request, en, key string, args ...any) string {
	if s.lang(r) == "ar" {
		return s.tr(r, key, args...)
	}
	return en
}

func (s *Server) brandTitle(r *http.Request, main string) string {
	return main + " · " + s.tr(r, "brand.name")
}

func (s *Server) u(r *http.Request, path string) string {
	if s.lang(r) == "en" && !s.isStatic {
		return "/en" + path
	}
	return path
}

func (s *Server) docPath(r *http.Request, path string, translated bool) string {
	if translated {
		return s.u(r, path)
	}
	return path
}

func stripLang(p string) string {
	for prefix := range langPrefixes {
		if strings.HasPrefix(p, prefix+"/") {
			return strings.TrimPrefix(p, prefix)
		}
	}
	return p
}
