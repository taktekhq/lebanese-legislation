package site

import (
	"context"
	"net/http"
	"sort"
	"sync"

	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
)

// A page's record of what it used, noted while it renders, for the static
// export (staticexport.Options.Record): the page template it executed
// ("set:law") and every UI string Go code looked up for it ("key:nav.home").
// The strings a template looks up itself are read from the template by the
// export's planner, not noted here. A later change to a template or a string
// then re-renders only the routes whose pages used it.
type depsKey struct{}

type pageDeps struct {
	mu    sync.Mutex
	names map[string]bool
}

func (d *pageDeps) note(name string) {
	d.mu.Lock()
	d.names[name] = true
	d.mu.Unlock()
}

// RecordDeps returns r carrying a fresh record, and a function that returns
// what the handler noted, sorted.
func RecordDeps(r *http.Request) (*http.Request, func() []string) {
	d := &pageDeps{names: map[string]bool{}}
	return r.WithContext(context.WithValue(r.Context(), depsKey{}, d)), func() []string {
		d.mu.Lock()
		defer d.mu.Unlock()
		out := make([]string, 0, len(d.names))
		for n := range d.names {
			out = append(out, n)
		}
		sort.Strings(out)
		return out
	}
}

func noteDep(ctx context.Context, name string) {
	if d, ok := ctx.Value(depsKey{}).(*pageDeps); ok {
		d.note(name)
	}
}

// bundle is the request's UI strings; a recorded request notes each lookup.
func (s *Server) bundle(r *http.Request) *i18n.Bundle {
	b := i18n.New(s.lang(r))
	if d, ok := r.Context().Value(depsKey{}).(*pageDeps); ok {
		return b.Noting(func(key string) { d.note("key:" + key) })
	}
	return b
}

// Route is the pattern the site answers a GET of path with, its /en or /ar
// prefix aside: the group the static export keeps a page's record under.
func (s *Server) Route(path string) string {
	r, err := http.NewRequest(http.MethodGet, stripLang(path), nil)
	if err != nil {
		return ""
	}
	_, pattern := s.mux.Handler(r)
	return pattern
}
