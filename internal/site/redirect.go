package site

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type moveRule struct {
	name string
	move func(s *Server, ctx context.Context, p string) (string, bool)
}

var moveRules = []moveRule{
	{"ar-prefix", (*Server).moveArPrefix},
	{"trailing-slash", (*Server).moveTrailingSlash},
	{"word", (*Server).moveWord},
	{"alias", (*Server).moveAlias},
	{"article", (*Server).moveArticle},
}

func (s *Server) finalPath(ctx context.Context, p string) string {
	for range 2 * len(moveRules) {
		moved := false
		for _, r := range moveRules {
			if q, ok := r.move(s, ctx, p); ok && q != p {
				p, moved = q, true
			}
		}
		if !moved {
			break
		}
	}
	return p
}

func (s *Server) redirectFinal(w http.ResponseWriter, r *http.Request, p string) {
	movedTo(w, r, s.finalPath(r.Context(), p))
}

func (s *Server) oneHop(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if p := r.URL.EscapedPath(); !strings.HasSuffix(p, "/") {
				if to := s.finalPath(r.Context(), p); to != p {
					movedTo(w, r, to)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func movedTo(w http.ResponseWriter, r *http.Request, to string) {
	if r.URL.RawQuery != "" {
		to += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, to, http.StatusMovedPermanently)
}

func (s *Server) moveArPrefix(_ context.Context, p string) (string, bool) {
	if p != "/ar" && !strings.HasPrefix(p, "/ar/") {
		return p, false
	}
	return "/" + strings.TrimLeft(p[len("/ar"):], "/"), true
}

func (s *Server) moveTrailingSlash(_ context.Context, p string) (string, bool) {
	if strings.HasSuffix(p, "/") || strings.Contains(p[strings.LastIndex(p, "/"):], ".") || s.mux == nil {
		return p, false
	}
	pattern := func(p string) string {
		u, err := url.ParseRequestURI(p)
		if err != nil {
			return ""
		}
		_, pattern := s.mux.Handler(&http.Request{Method: http.MethodGet, URL: u, Host: "lebaneselegislation.com"})
		return pattern
	}
	bare := pattern(p)
	route := strings.TrimSuffix(bare, "{$}")

	toSlashed := bare == pattern(p+"/") && strings.HasSuffix(route, "/") &&
		(route != bare || strings.HasSuffix(bare, " "+p+"/"))
	if bare != "GET /" && !toSlashed {
		return p, false
	}
	return p + "/", true
}

func textAddress(p string) (word string, id int, rest string, ok bool) {
	parts := strings.SplitN(p, "/", 4)
	if len(parts) < 4 {
		return "", 0, "", false
	}
	for _, w := range addressWords {
		if parts[1] == w {
			if id, err := strconv.Atoi(parts[2]); err == nil && id > 0 {
				return w, id, parts[3], true
			}
		}
	}
	return "", 0, "", false
}

func (s *Server) moveWord(_ context.Context, p string) (string, bool) {
	word, id, rest, ok := textAddress(p)
	if !ok || word == textWord(id) {
		return p, false
	}
	return lawPath(id) + rest, true
}

func (s *Server) moveAlias(ctx context.Context, p string) (string, bool) {
	_, id, rest, ok := textAddress(p)
	if !ok || id < scanIDBase || s.db == nil {
		return p, false
	}
	var to int
	if err := s.db.QueryRow(ctx, `SELECT law_id FROM legislation_alias a WHERE a.id = $1
		AND NOT EXISTS (SELECT 1 FROM legislation l WHERE l.id = a.id)`, id).Scan(&to); err != nil {
		return p, false
	}
	if strings.HasPrefix(rest, "article/") {
		return lawPath(to) + rest, true
	}
	return lawPath(to), true
}

func (s *Server) moveArticle(ctx context.Context, p string) (string, bool) {
	_, id, rest, ok := textAddress(p)
	if !ok || !strings.HasPrefix(rest, "article/") || s.db == nil {
		return p, false
	}
	seg, tail, found := strings.Cut(rest[len("article/"):], "/")
	if !found {
		return p, false
	}
	want, err := url.PathUnescape(seg)
	if err != nil {
		return p, false
	}
	rows, err := s.db.Query(ctx, "SELECT number FROM legislation_article WHERE law_id = $1 ORDER BY position", id)
	if err != nil {
		return p, false
	}
	numbers, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return p, false
	}
	for _, n := range numbers {
		if Slug(n) == want {
			return p, false
		}
	}
	if n := articleNamed(want, numbers); n != "" && Slug(n) != want {
		return artPath(id, n) + tail, true
	}
	return p, false
}
