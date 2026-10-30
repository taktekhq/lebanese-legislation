package site

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const (
	partBudget = 700_000

	chapterBytes = 400
)

type LawPart struct {
	N        int
	First    string
	Last     string
	Path     string
	Articles int
}

func (p LawPart) Current(of int) bool { return p.N == of }

func splitItems(sizes []int, chapter []bool, budget int) [][2]int {
	var out [][2]int
	start, cur := 0, 0
	for i, n := range sizes {
		if cur+n > budget && i > start && !chapter[i] {
			cut := i
			for cut > start+1 && chapter[cut-1] {
				cut--
			}
			if cut > start {
				out = append(out, [2]int{start, cut})
				start = cut
				cur = 0
				for j := cut; j < i; j++ {
					cur += sizes[j]
				}
			}
		}
		cur += n
	}
	return append(out, [2]int{start, len(sizes)})
}

func (s *Server) itemSizes(r *http.Request, l *Law) (sizes []int, chapter []bool, err error) {
	pages := s.pages
	if s.lang(r) == "ar" && s.pagesAr != nil {
		pages = s.pagesAr
	}
	for _, it := range l.Items {
		if it.Chapter != nil {
			sizes, chapter = append(sizes, chapterBytes), append(chapter, true)
			continue
		}
		var b strings.Builder
		if err := pages["law"].ExecuteTemplate(&b, "article", map[string]any{"Law": l, "A": it.Article, "Standalone": false}); err != nil {
			return nil, nil, err
		}
		sizes, chapter = append(sizes, b.Len()), append(chapter, false)
	}
	return sizes, chapter, nil
}

func (s *Server) partsOf(r *http.Request, l *Law, fullLen int) ([][2]int, error) {
	if fullLen > 0 && fullLen <= partBudget {
		return nil, nil
	}
	sizes, chapter, err := s.itemSizes(r, l)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, n := range sizes {
		total += n
	}
	if total <= partBudget {
		return nil, nil
	}
	return splitItems(sizes, chapter, budgetFor(total)), nil
}

func budgetFor(total int) int {
	n := (total + partBudget - 1) / partBudget
	return min(partBudget, total/n+total/(n*20)+1)
}

func (s *Server) lawPart(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("part"))
	if err != nil || n < 1 {
		s.notFound(w, r)
		return
	}
	s.lawPage(w, r, n)
}

func (s *Server) serveLaw(w http.ResponseWriter, r *http.Request, l *Law, p Page, n int) {
	full := ""
	if n <= 1 {
		var err error
		if full, err = s.execute(r, "law", p); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	runs, err := s.partsOf(r, l, len(full))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	switch {
	case len(runs) <= 1 && n == 0:
		s.write(w, r, 200, full)
		return
	case len(runs) <= 1, n > len(runs):
		s.notFound(w, r)
		return
	case n == 1:
		http.Redirect(w, r, s.u(r, lawPath(l.ID)), http.StatusMovedPermanently)
		return
	}
	if n == 0 {
		n = 1
	}
	items := l.Items
	for i, run := range runs {
		first, last := "", ""
		count := 0
		for _, it := range items[run[0]:run[1]] {
			if it.Article == nil {
				continue
			}
			if first == "" {
				first = it.Article.Number
			}
			last = it.Article.Number
			count++
		}
		l.Parts = append(l.Parts, LawPart{N: i + 1, First: first, Last: last, Articles: count,
			Path: pagePartPath(l.ID, i+1)})
		for _, it := range items[run[0]:run[1]] {
			if it.Chapter != nil {
				if l.SectionPart == nil {
					l.SectionPart = map[int]int{}
				}
				l.SectionPart[it.Chapter.ID] = i + 1
			}
		}
	}
	l.Part = n
	l.Items = items[runs[n-1][0]:runs[n-1][1]]
	if n > 1 {
		l.Preamble, l.ExplanatoryNote = nil, nil
		p.Title += " · " + s.tr(r, "law.part_n", strconv.Itoa(n))
		p.Path = pagePartPath(l.ID, n)
		p.Desc = cut(l.Title+": "+s.tr(r, "law.part_desc", strconv.Itoa(n), strconv.Itoa(len(runs))), 155)
	}
	if n < len(runs) {
		l.Closing = nil
	}
	s.render(w, r, "law", 200, p)
}

func pagePartPath(id, n int) string { return fmt.Sprintf("%spart/%d/", lawPath(id), n) }

func (l *Law) SectionPartPath(id int) string {
	if n := l.SectionPart[id]; n != 0 && n != l.Part {
		if n == 1 {
			return lawPath(l.ID)
		}
		return pagePartPath(l.ID, n)
	}
	return ""
}
