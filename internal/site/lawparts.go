package site

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// A very long text is read in parts. Googlebot reads the first 2 MB of a page
// and /law/244736/ was 2.2 MB, so a text whose articles come to more than
// partBudget bytes of HTML is cut into parts of about that size: the text's
// own address holds the first, /law/{id}/part/2/ the next, each linked from
// all of the others and each article still on its own page. Nothing is
// hidden behind a script: every part is plain HTML.

const (
	// partBudget is the HTML of the articles in one part. With the facts at
	// the top (20 to 300 KB) a part stays under 1 MB.
	partBudget = 700_000
	// chapterBytes is what a chapter heading adds.
	chapterBytes = 400
)

// LawPart is one part of a long text, for the links between them.
type LawPart struct {
	N        int
	First    string // number of its first article
	Last     string
	Path     string
	Articles int
}

// Current says whether this is the part being shown.
func (p LawPart) Current(of int) bool { return p.N == of }

// splitItems cuts a text's items (chapter headings and articles, with the
// bytes each takes) into runs of at most budget bytes. A run holds at least
// one article, and a chapter heading goes with the article after it, never
// stranded at the end of a part.
func splitItems(sizes []int, chapter []bool, budget int) [][2]int {
	var out [][2]int
	start, cur := 0, 0
	for i, n := range sizes {
		if cur+n > budget && i > start && !chapter[i] {
			cut := i
			for cut > start+1 && chapter[cut-1] {
				cut--
			}
			if cut > start { // the run has an article before the chapters
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

// itemSizes is the HTML each item of a text takes on its page.
func (s *Server) itemSizes(r *http.Request, l *Law) (sizes []int, chapter []bool, err error) {
	noteDep(r.Context(), "set:law") // the law template's article sizes split long laws into parts
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

// partsOf cuts the text into parts, or returns nil when one page holds it.
// full is the HTML of the whole page when it has been rendered (a text of
// under partBudget bytes in all cannot need cutting).
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

// budgetFor evens the parts out: a text of 1.2 MB is two parts of 600 KB, not
// a full one and a sliver.
func budgetFor(total int) int {
	n := (total + partBudget - 1) / partBudget
	return min(partBudget, total/n+total/(n*20)+1) // a little over the mean, so n runs hold it
}

// lawPart serves /law/{id}/part/{n}/.
func (s *Server) lawPart(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("part"))
	if err != nil || n < 1 {
		s.notFound(w, r)
		return
	}
	s.lawPage(w, r, n)
}

// serveLaw writes a text's page, or its part n (n 0 is the text's own
// address and part 1 when it is cut).
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
		s.notFound(w, r) // /part/N/ of a text that is not cut, or past its last part
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

// pagePartPath is part n's address.
func pagePartPath(id, n int) string { return fmt.Sprintf("%spart/%d/", lawPath(id), n) }

// SectionPartPath is where a chapter is when it is in another part of the
// text, "" when it is on this page.
func (l *Law) SectionPartPath(id int) string {
	if n := l.SectionPart[id]; n != 0 && n != l.Part {
		if n == 1 {
			return lawPath(l.ID)
		}
		return pagePartPath(l.ID, n)
	}
	return ""
}
