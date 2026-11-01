// SPDX-License-Identifier: MIT

package linkdepth

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Row is the sample of one family of pages.
type Row struct {
	Family     string
	N          int
	Within3    int
	Within5    int
	Unreached  int
	ZeroInlink int
	MedInlinks int
	Hist       map[int]int
}

// Summarize scores each sampled path by its depth and in-links in a crawl.
// A path the crawl never met is unreached; its in-links are zero.
func Summarize(sample []string, res *Result) []Row {
	by := map[string]*Row{}
	var all Row
	all.Family, all.Hist = "all", map[int]int{}
	ins := map[string][]int{}
	add := func(r *Row, fam string, d int, found bool, in int) {
		r.N++
		if !found {
			r.Unreached++
		} else {
			r.Hist[d]++
			if d <= 3 {
				r.Within3++
			}
			if d <= 5 {
				r.Within5++
			}
		}
		if in == 0 {
			r.ZeroInlink++
		}
		ins[fam] = append(ins[fam], in)
	}
	for _, p := range sample {
		fam := Family(p)
		r := by[fam]
		if r == nil {
			r = &Row{Family: fam, Hist: map[int]int{}}
			by[fam] = r
		}
		d, found := res.Depth[p]
		in := len(res.Inlinks[p])
		add(r, fam, d, found, in)
		add(&all, "all", d, found, in)
	}
	var rows []Row
	for fam, r := range by {
		r.MedInlinks = median(ins[fam])
		rows = append(rows, *r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Family < rows[j].Family })
	all.MedInlinks = median(ins["all"])
	return append(rows, all)
}

func median(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	return s[len(s)/2]
}

// WriteTable prints rows as a Markdown table.
func WriteTable(w io.Writer, rows []Row) {
	_, _ = fmt.Fprintln(w, "| family | sampled | within 3 clicks | within 5 clicks | not reached | no in-link | median in-links | depth histogram |")
	_, _ = fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|---:|---|")
	for _, r := range rows {
		var ds []int
		for d := range r.Hist {
			ds = append(ds, d)
		}
		sort.Ints(ds)
		var h []string
		for _, d := range ds {
			h = append(h, fmt.Sprintf("%d:%d", d, r.Hist[d]))
		}
		_, _ = fmt.Fprintf(w, "| %s | %d | %d | %d | %d | %d | %d | %s |\n", r.Family, r.N, r.Within3, r.Within5, r.Unreached, r.ZeroInlink, r.MedInlinks, strings.Join(h, " "))
	}
}
