package site

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Old addresses move in one hop. The site's addresses changed three times:
// /law/ /decree/ /text/ by kind (16 Oct), /ar/ to the root (21 Oct), and one
// address per article number (24 Oct). Each move used to redirect on its own,
// so an old link indexed as /ar/law/198100/article/3/ went to /law/... and
// then to /decree/...: two 301s, and Google drops a URL that bounces twice.
// Every redirect for an old address now goes through finalPath, which applies
// all the moves at once and answers the page's own address.

// moveRule is one move: it rewrites an escaped path (no query) and says
// whether it changed it.
type moveRule struct {
	name string
	move func(s *Server, ctx context.Context, p string) (string, bool)
}

// moveRules is the redirect table. The first three need no database; alias
// and article read it, and do nothing on a server without one.
var moveRules = []moveRule{
	{"ar-prefix", (*Server).moveArPrefix},
	{"trailing-slash", (*Server).moveTrailingSlash},
	{"word", (*Server).moveWord},
	{"alias", (*Server).moveAlias},
	{"article", (*Server).moveArticle},
}

// finalPath is where an old escaped path ends up once every move has run:
// the rules are applied until none changes it.
func (s *Server) finalPath(ctx context.Context, p string) string {
	p = oneLeadingSlash(p)
	for range 2 * len(moveRules) { // each rule moves a path at most once; the bound only guards a bug
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

// redirectFinal sends a 301 to the final address of p, keeping the query.
func (s *Server) redirectFinal(w http.ResponseWriter, r *http.Request, p string) {
	movedTo(w, r, s.finalPath(r.Context(), p))
}

// oneHop answers a GET or HEAD for a page address without its final slash
// before the mux does: the mux would send /law/1/article/3 on with a 307 to
// /law/1/article/3/, and that to /decree/..., where this is one 301.
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

// oneLeadingSlash makes p start with exactly one slash. A path that starts
// with "//" or "/\\" is read by browsers as another host, so no redirect may
// answer one: "//host/x" is the path "/host/x" here.
func oneLeadingSlash(p string) string {
	return "/" + strings.TrimLeft(p, "/\\")
}

// movedTo sends a 301 to the escaped path to, keeping the query. The path
// always stays on this site.
func movedTo(w http.ResponseWriter, r *http.Request, to string) {
	to = oneLeadingSlash(to)
	if r.URL.RawQuery != "" {
		to += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, to, http.StatusMovedPermanently)
}

// moveArPrefix: /ar/... is the same path at the root. Leading slashes are
// collapsed so "/ar//host" cannot become a protocol-relative address.
func (s *Server) moveArPrefix(_ context.Context, p string) (string, bool) {
	if p != "/ar" && !strings.HasPrefix(p, "/ar/") {
		return p, false
	}
	return "/" + strings.TrimLeft(p[len("/ar"):], "/"), true
}

// moveTrailingSlash: a page address without its final slash (/laws) gets
// it, when its last segment is not a file and no route answers the bare
// path: either nothing does (the catch-all) or the mux would only send it
// on to the slashed route, with a 307 of its own.
func (s *Server) moveTrailingSlash(_ context.Context, p string) (string, bool) {
	if !strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") || strings.Contains(p[strings.LastIndex(p, "/"):], ".") || s.mux == nil {
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
	// the mux's own redirect, which a subtree route ("GET /.well-known/") makes
	// only for its own root: it answers every deeper path itself
	toSlashed := bare == pattern(p+"/") && strings.HasSuffix(route, "/") &&
		(route != bare || strings.HasSuffix(bare, " "+p+"/"))
	if bare != "GET /" && !toSlashed {
		return p, false // a route answers it (the API, /healthz, a file under /ul/uploads/images/)
	}
	return p + "/", true
}

// textAddress splits /{word}/{id}/rest into its parts.
func textAddress(p string) (word string, id int, rest string, ok bool) {
	parts := strings.SplitN(p, "/", 4) // "", word, id, rest
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

// moveWord: a text named with the wrong word (/law/ for a decree) is at its
// own word, the rest of the path kept.
func (s *Server) moveWord(_ context.Context, p string) (string, bool) {
	word, id, rest, ok := textAddress(p)
	if !ok || word == textWord(id) {
		return p, false
	}
	return lawPath(id) + rest, true
}

// moveAlias: the old address of a scanned text the Centre has since
// published is the Centre's text; an article address keeps its article.
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

// moveArticle: another spelling of an article number the text has ("٣",
// "12/1", "12-bis") is the address of the article it names.
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
