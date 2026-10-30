package site

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

func (s *Server) canonical(r *http.Request, p Page) (string, bool) {
	if s.isStatic {
		return p.Path, false
	}
	if p.Lang == "ar" && !p.Translated {
		return p.Path, false
	}
	return s.u(r, p.Path), true
}

func (s *Server) noindexed(ctx context.Context, kind string, id, pos int) bool {
	var one int
	return s.db.QueryRow(ctx, `SELECT 1 FROM noindex_page WHERE kind = $1 AND id = $2 AND position = $3`, kind, id, pos).Scan(&one) == nil
}

const yearPageSize = 300

func pagePath(base string, n int) string {
	if n <= 1 {
		return base
	}
	return base + "page/" + strconv.Itoa(n) + "/"
}

func pageNums(page, pages int) []int {
	if pages <= 1 {
		return nil
	}
	want := map[int]bool{1: true, pages: true}
	reach := 2
	if pages > 12 {
		reach = 5
	}
	for i := page - reach; i <= page+reach; i++ {
		want[i] = true
	}
	if pages > 12 {
		step := 10
		if pages > 300 {
			step = 100
		}
		for i := step; i < pages; i += step {
			want[i] = true
		}
		if pages > 300 {
			for i := (page - 50) / 10 * 10; i <= page+50 && i < pages; i += 10 {
				want[i] = true
			}
		}
	} else {
		for i := 1; i <= pages; i++ {
			want[i] = true
		}
	}
	var ns []int
	for n := range want {
		if n >= 1 && n <= pages {
			ns = append(ns, n)
		}
	}
	sort.Ints(ns)
	var out []int
	for i, n := range ns {
		if i > 0 && n != ns[i-1]+1 {
			out = append(out, 0)
		}
		out = append(out, n)
	}
	return out
}

func (s *Server) pageArg(w http.ResponseWriter, r *http.Request, base string, q url.Values, pages int) (page int, done bool) {
	pages = max(pages, 1)
	if v := r.PathValue("page"); v != "" {
		n, err := strconv.Atoi(v)
		switch {
		case err != nil || n < 1 || n > pages:
			s.notFound(w, r)
			return 0, true
		case n == 1:
			http.Redirect(w, r, s.u(r, base), http.StatusMovedPermanently)
			return 0, true
		}
		return n, false
	}
	if _, has := q["page"]; has && len(q) == 1 && !s.isStatic {
		n, _ := strconv.Atoi(q.Get("page"))
		http.Redirect(w, r, s.u(r, pagePath(base, min(max(n, 1), pages))), http.StatusMovedPermanently)
		return 0, true
	}
	return min(pageNo(q), pages), false
}

func (s *Server) pageSuffix(r *http.Request, page int) string {
	if page <= 1 {
		return ""
	}
	return " · " + fmt.Sprint(s.tr(r, "pager.page_n", thousands(page)))
}

func (s *Server) listPaging(w http.ResponseWriter, r *http.Request, base string, q url.Values, total, size int) (page, pages int, listBase string, done bool) {
	pages = max(1, (total+size-1)/size)
	if r.PathValue("page") != "" && len(q) > 0 {
		s.notFound(w, r)
		return 0, pages, "", true
	}
	page, done = s.pageArg(w, r, base, q, pages)
	if done {
		return 0, pages, "", true
	}
	if len(q) == 0 && !s.isStatic {
		listBase = base
	}
	return page, pages, listBase, false
}
