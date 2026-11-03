package staticexport

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
)

// ExpandChangedPaths includes old pagination/parts for deletion and learns new
// parts from each selected law's handler. It follows only links to parts of the
// same law, never a general crawl. Every shard computes the same list before
// Owner assigns paths, so newly split parts cannot be lost across owners.
func ExpandChangedPaths(h http.Handler, paths []string, prev map[string]Entry) ([]string, error) {
	selected := map[string]bool{}
	roots := map[string]bool{}
	lists := map[string]bool{}
	for _, p := range paths {
		selected[p] = true
		q := strings.TrimPrefix(p, "/en")
		s := strings.Split(strings.Trim(q, "/"), "/")
		if len(s) == 2 && (s[0] == "law" || s[0] == "decree" || s[0] == "text") {
			roots[p] = true
		}
		if strings.Contains(q, "/year/") || strings.HasPrefix(q, "/codes/") || q == "/opinions/" || q == "/agreements/" || q == "/doctrine/" || q == "/research/" {
			if !strings.Contains(p, "/page/") {
				lists[p] = true
			}
		}
	}
	for p := range prev {
		for root := range roots {
			if strings.HasPrefix(p, root+"part/") {
				selected[p] = true
			}
		}
		for base := range lists {
			if strings.HasPrefix(p, base+"page/") {
				selected[p] = true
			}
		}
	}
	for root := range roots {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, root, nil))
		if rec.Code >= 500 {
			return nil, fmt.Errorf("discovering parts of %s: status %d", root, rec.Code)
		}
		if rec.Code != 200 {
			continue
		}
		for _, p := range Links(rec.Body.Bytes()) {
			if strings.HasPrefix(p, root+"part/") {
				selected[p] = true
			}
		}
	}
	out := make([]string, 0, len(selected))
	for p := range selected {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}
