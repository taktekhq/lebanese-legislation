// Package static saves the site as plain files for a static host such as
// GitHub Pages.
//
// Every page is rendered by the same server code, in process, so the copy can
// never drift from the live site; only the static mode's differences apply
// (see site.NewStatic). Links are rewritten for the path the copy is served
// under, and a title index is written for in-browser search.
package static

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/site"
)

// Stats is what a build wrote.
type Stats struct {
	Pages int64
	Bytes int64
}

// Build writes the site into out, for serving under base ("" for a domain
// root, "/lebaneselegislation" for a GitHub project page).
func Build(ctx context.Context, pool *pgxpool.Pool, out, base string, workers int, log func(string, ...any)) (Stats, error) {
	var st Stats
	srv, err := site.NewStatic(pool)
	if err != nil {
		return st, err
	}
	base = strings.TrimRight(base, "/")
	paths, err := routes(ctx, pool, srv)
	if err != nil {
		return st, err
	}
	log("%d pages to write", len(paths))

	jobs := make(chan string)
	var wg sync.WaitGroup
	var first error
	var once sync.Once
	for w := 0; w < max(workers, 1); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				n, err := page(srv, out, base, p)
				if err != nil {
					once.Do(func() { first = err })
					continue
				}
				if c := atomic.AddInt64(&st.Pages, 1); c%20000 == 0 {
					log("%d pages", c)
				}
				atomic.AddInt64(&st.Bytes, n)
			}
		}()
	}
	for _, p := range paths {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	if first != nil {
		return st, first
	}

	// The not-found page, which GitHub Pages serves for any missing path.
	if n, err := write(srv, out, base, "/__not_found__/", filepath.Join(out, "404.html"), true); err != nil {
		return st, err
	} else {
		st.Bytes += n
	}
	// Stylesheets, scripts, images.
	err = fs.WalkDir(site.StaticFiles(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(site.StaticFiles(), p)
		if err != nil {
			return err
		}
		st.Bytes += int64(len(b))
		return save(filepath.Join(out, p), b)
	})
	if err != nil {
		return st, err
	}
	n, err := searchIndex(ctx, pool, filepath.Join(out, "search-index.json"))
	if err != nil {
		return st, err
	}
	st.Bytes += n
	// A preview: kept out of search engines, and served as-is (no Jekyll).
	if err := save(filepath.Join(out, "robots.txt"), []byte("User-agent: *\nDisallow: /\n")); err != nil {
		return st, err
	}
	return st, save(filepath.Join(out, ".nojekyll"), nil)
}

// routes lists every page of the static site.
func routes(ctx context.Context, pool *pgxpool.Pool, srv *site.Server) ([]string, error) {
	paths := []string{"/", "/codes/", "/changes/", "/about/", "/ai/", "/developers/", "/data/", "/search/",
		"/laws/", "/rulings/", "/gazette/", "/laws/years/", "/rulings/years/", "/gazette/years/",
		"/opinions/", "/agreements/", "/doctrine/", "/research/"}
	for _, k := range []string{"laws", "rulings", "gazette"} {
		ys, err := srv.Years(ctx, k)
		if err != nil {
			return nil, err
		}
		for _, y := range ys {
			paths = append(paths, fmt.Sprintf("/%s/year/%d/", k, y.Year))
		}
	}
	for prefix, sql := range map[string]string{
		"/law/":       "SELECT id FROM legislation",
		"/ruling/":    "SELECT id FROM ruling",
		"/gazette/":   "SELECT id FROM gazette_issue",
		"/opinion/":   "SELECT id FROM opinion",
		"/agreement/": "SELECT id FROM agreement",
		"/doctrine/":  "SELECT id FROM doctrine_article",
		"/research/":  "SELECT id FROM research",
	} {
		rows, err := pool.Query(ctx, sql+" ORDER BY id")
		if err != nil {
			return nil, err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int32])
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			paths = append(paths, fmt.Sprintf("%s%d/", prefix, id))
		}
	}
	return paths, nil
}

func page(srv *site.Server, out, base, p string) (int64, error) {
	return write(srv, out, base, p, filepath.Join(out, filepath.FromSlash(strings.Trim(p, "/")), "index.html"), false)
}

// write renders one path and saves it with its links rewritten.
func write(srv *site.Server, out, base, p, file string, notFound bool) (int64, error) {
	req := httptest.NewRequest(http.MethodGet, p, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && !(notFound && rec.Code == http.StatusNotFound) {
		return 0, fmt.Errorf("%s: status %d", p, rec.Code)
	}
	b := rewrite(rec.Body.Bytes(), base)
	return int64(len(b)), save(file, b)
}

// Root-relative links in pages, to be served under a base path.
var local = regexp.MustCompile(`(\s(?:href|src|action|data-href|data-index|data-base)=")/([^/])`)

func rewrite(b []byte, base string) []byte {
	b = compact(b)
	if base == "" {
		return b
	}
	return local.ReplaceAll(b, []byte("${1}"+base+"/${2}"))
}

var (
	indent = regexp.MustCompile(`\n[ \t]+`)
	blanks = regexp.MustCompile(`\n{2,}`)
)

// compact drops the templates' indentation and blank lines: across 160,000
// pages it adds up. A line break stays wherever there was whitespace, so the
// spacing between words never changes; <pre> blocks are left as they are.
func compact(b []byte) []byte {
	var out []byte
	for len(b) > 0 {
		i := bytes.Index(b, []byte("<pre"))
		if i < 0 {
			i = len(b)
		}
		chunk := indent.ReplaceAll(b[:i], []byte("\n"))
		out = append(out, blanks.ReplaceAll(chunk, []byte("\n"))...)
		b = b[i:]
		if len(b) == 0 {
			break
		}
		j := bytes.Index(b, []byte("</pre>"))
		if j < 0 {
			j = len(b)
		} else {
			j += len("</pre>")
		}
		out = append(out, b[:j]...)
		b = b[j:]
	}
	return out
}

func save(file string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o644)
}

// searchIndex writes every text's title, kind, number, year and status for
// the in-browser search, as compact arrays.
func searchIndex(ctx context.Context, pool *pgxpool.Pool, file string) (int64, error) {
	rows, err := pool.Query(ctx, `SELECT name_ar, coalesce(name_en, '') FROM legislation_kind ORDER BY name_ar`)
	if err != nil {
		return 0, err
	}
	kinds := [][2]string{}
	kindIdx := map[string]int{}
	for rows.Next() {
		var k [2]string
		if err := rows.Scan(&k[0], &k[1]); err != nil {
			return 0, err
		}
		kindIdx[k[0]] = len(kinds)
		kinds = append(kinds, k)
	}
	rows, err = pool.Query(ctx, `SELECT l.id, l.title, l.kind, coalesce(l.number, ''),
		coalesce(extract(year FROM l.issued_on)::int, 0), l.status = 'repealed',
		EXISTS (SELECT 1 FROM legislation_article a WHERE a.law_id = l.id),
		coalesce((SELECT name_en FROM code c WHERE c.law_id = l.id), '')
		FROM legislation l ORDER BY l.id`)
	if err != nil {
		return 0, err
	}
	var laws [][]any
	for rows.Next() {
		var id, year int
		var title, kind, number, code string
		var repealed, text bool
		if err := rows.Scan(&id, &title, &kind, &number, &year, &repealed, &text, &code); err != nil {
			return 0, err
		}
		laws = append(laws, []any{id, title, kindIdx[kind], number, year, b2i(repealed), b2i(text), code})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	b, err := json.Marshal(map[string]any{"kinds": kinds, "laws": laws})
	if err != nil {
		return 0, err
	}
	return int64(len(b)), save(file, b)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
