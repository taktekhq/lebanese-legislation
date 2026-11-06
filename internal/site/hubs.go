package site

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// Hub pages: lists whose job is to link. Every article of a code is on a page
// of its own code hub, and every text of a year is on a page of the hub of its
// kind and year, so a crawler that has read the codes page or the years page
// is two clicks from them, without the sitemap. Both are plain <a href> lists
// with path pagination (robots.txt blocks ?page=), self-canonical, an
// English/Arabic pair like the year lists. Nothing here changes an existing
// address.

// codeHubSize is how many articles a code hub page lists: a page is about 100
// links under their chapter headings.
const codeHubSize = 100

// HubGroup is the articles of one chapter that fall on a hub page.
type HubGroup struct {
	Heading string // the chapter's heading, "" before the first one
	Numbers []string
}

// CodeHub is one page of a code's article list.
type CodeHub struct {
	List
	LawID         int
	Slug          string
	Title         string // the code's Arabic title
	NameEn        string
	NameFr        *string
	Heading, Lead string
	First, Last   string
	Groups        []HubGroup
}

func init() {
	baseFuncs["kindWord"] = wordForKind
	baseFuncs["derefStr"] = func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
}

func (s *Server) hubRoutes() {
	if s.isStatic {
		return
	}
	m := s.mux
	m.HandleFunc("GET /codes/{slug}/{$}", s.codeHub)
	m.HandleFunc("GET /codes/{slug}/page/{page}/{$}", s.codeHub)
	m.HandleFunc("GET /laws/year/{year}/{word}/{$}", s.yearKindHub)
	m.HandleFunc("GET /laws/year/{year}/{word}/page/{page}/{$}", s.yearKindHub)
	s.AddCoreSitemap(s.hubSitemapRows)
}

// codeHubPath is page n of a code's hub.
func codeHubPath(slug string, n int) string { return pagePath("/codes/"+slug+"/", n) }

// codeHubPage is the hub page that lists the article at index i of its text.
func codeHubPage(i int) int { return i/codeHubSize + 1 }

func (s *Server) codeHub(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	slug := r.PathValue("slug")
	var c CodeHub
	c.Slug = slug
	if err := s.db.QueryRow(ctx, `SELECT c.law_id, c.name_en, c.name_fr, l.title,
		(SELECT count(*) FROM legislation_article a WHERE a.law_id = c.law_id)::int
		FROM code c JOIN legislation l ON l.id = c.law_id WHERE c.slug = $1`, slug).
		Scan(&c.LawID, &c.NameEn, &c.NameFr, &c.Title, &c.Total); err != nil {
		s.notFound(w, r)
		return
	}
	base := "/codes/" + slug + "/"
	page, pages, listBase, done := s.listPaging(w, r, base, r.URL.Query(), c.Total, codeHubSize)
	if done {
		return
	}
	// The only numbered pages left (keyset.go): page n is articles
	// (n-1)*100+1 to n*100 by position, which runs 1..n in every text, so
	// it is a range on the primary key (law_id, position), not an OFFSET.
	rows, err := s.db.Query(ctx, `SELECT a.number, coalesce(a.section_id, 0), coalesce(sec.heading, '')
		FROM legislation_article a LEFT JOIN legislation_section sec ON sec.id = a.section_id
		WHERE a.law_id = $1 AND a.position > $2 AND a.position <= $3 ORDER BY a.position`, c.LawID, (page-1)*codeHubSize, page*codeHubSize)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	prev := -1
	for rows.Next() {
		var number, heading string
		var sec int
		if err := rows.Scan(&number, &sec, &heading); err != nil {
			s.fail(w, r, err)
			return
		}
		if sec != prev || len(c.Groups) == 0 {
			c.Groups = append(c.Groups, HubGroup{Heading: Heading(heading)})
			prev = sec
		}
		g := &c.Groups[len(c.Groups)-1]
		g.Numbers = append(g.Numbers, number)
		if c.First == "" {
			c.First = number
		}
		c.Last = number
	}
	if err := rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	c.List = List{Total: c.Total, Page: page, Pages: pages, Base: listBase,
		Updated: s.pageUpdated(r, s.changedOn(ctx, "law", c.LawID), nil)}
	name := c.NameEn
	if s.lang(r) == "ar" {
		name = cut(c.Title, 36)
	}
	c.Heading = s.trOr(r, name+", article by article.", "hub.code.h1", name)
	c.Lead = s.trOr(r, fmt.Sprintf("Articles %s to %s of %s, in order. Each one opens on its own page, with the rulings that cite it.",
		c.First, c.Last, c.NameEn), "hub.code.lead", c.First, c.Last, cut(c.Title, 60))
	title := s.trOr(r, fmt.Sprintf("%s: articles %s to %s", cut(c.NameEn, 34), c.First, c.Last), "hub.code.title", cut(c.Title, 30), c.First, c.Last)
	desc := s.trOr(r, fmt.Sprintf("Articles %s to %s of the %s, in order, each linked to its own page. %s articles in all. Free.",
		c.First, c.Last, c.NameEn, thousands(c.Total)), "hub.code.desc", c.First, c.Last, cut(c.Title, 60), thousands(c.Total))
	s.render(w, r, "codehub", 200, Page{
		Title: s.brandTitle(r, title) + s.pageSuffix(r, page), Desc: desc, Path: pagePath(base, page),
		LD:   []any{breadcrumbs("Codes", "/codes/", cut(c.NameEn, 60), base)},
		Data: c})
}

// kindYearWords are the address words a year's texts are split by, and the
// SQL that tells which word a text has (words.go: wordForKind).
const kindWordSQL = `CASE WHEN l.kind LIKE 'مرسوم%' THEN 'decree'
	WHEN l.kind LIKE 'قانون%' OR l.kind IN ('الدستور', 'دستور') THEN 'law' ELSE 'text' END`

var kindYearWords = []string{"law", "decree", "text"}

func validWord(w string) bool {
	for _, k := range kindYearWords {
		if k == w {
			return true
		}
	}
	return false
}

func kindYearPath(year int, word string) string { return fmt.Sprintf("/laws/year/%d/%s/", year, word) }

// yearKinds is how many texts of each word a year has, for the links on the
// year's own page.
func (s *Server) yearKindCounts(ctx context.Context, year int) (map[string]int, error) {
	rows, err := s.db.Query(ctx, `SELECT `+kindWordSQL+` AS word, count(*)::int FROM legislation l
		WHERE l.issued_on >= make_date($1, 1, 1) AND l.issued_on < make_date($1 + 1, 1, 1) GROUP BY 1`, year)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for rows.Next() {
		var w string
		var n int
		if err := rows.Scan(&w, &n); err != nil {
			rows.Close()
			return nil, err
		}
		out[w] = n
	}
	return out, rows.Err()
}

// kindYearChips are the links from a year's page to its kind hubs.
func (s *Server) kindYearChips(r *http.Request, year int, counts map[string]int) []Chip {
	var chips []Chip
	for _, w := range kindYearWords {
		if counts[w] > 0 {
			chips = append(chips, Chip{Value: kindYearPath(year, w), Label: s.tr(r, "hub.kind."+w), Count: counts[w]})
		}
	}
	return chips
}

func (s *Server) yearKindHub(w http.ResponseWriter, r *http.Request) {
	y, ok := yearParam(r)
	word := r.PathValue("word")
	if !ok || y == 0 || !validWord(word) {
		s.notFound(w, r)
		return
	}
	pg, done := s.cursorPaging(w, r, true)
	if done {
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	key := dateKey("l.issued_on")
	cond := key + ` >= make_date($1, 1, 1) AND ` + key + ` < make_date($1 + 1, 1, 1) AND ` + kindWordSQL + ` = $2`
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*)::int FROM legislation l WHERE `+cond, y, word).Scan(&total); err != nil {
		s.fail(w, r, err)
		return
	}
	if total == 0 {
		s.notFound(w, r)
		return
	}
	base := kindYearPath(y, word)
	args := []any{y, word}
	page := pg.where(key, "l.id", &args)
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM (SELECT l.id FROM legislation l`+and(cond, page)+pg.order(key, "l.id")+
		fmt.Sprintf(" LIMIT %d", s.yearSize()+1)+`) p JOIN legislation l ON l.id = p.id JOIN legislation_kind k ON k.name_ar = l.kind`+pg.order(key, "l.id"), args...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := scanLawRows(rows)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, olderOK, newerOK, top := settle(pg, list, s.yearSize())
	if top {
		s.toFirstPage(w, r)
		return
	}
	older, newer := lawPageLinks(r.URL.Query(), list, olderOK, newerOK)
	label := strconv.Itoa(y)
	kind := s.tr(r, "hub.kind."+word)
	enKind := i18nKindEn[word]
	title := s.trOr(r, fmt.Sprintf("%s of %s", enKind, label), "hub.year.title", kind, label)
	s.render(w, r, "laws", 200, Page{
		Title: s.brandTitle(r, title),
		Path:  base, NoIndex: !pg.first(),
		Desc: s.trOr(r, fmt.Sprintf("The %s %s issued in Lebanon in %s, newest first, each linked to its text. Free.", thousands(total), lowerFirst(enKind), label),
			"hub.year.desc", thousands(total), kind, label),
		LD: []any{breadcrumbs("Legislation", "/laws/", label, fmt.Sprintf("/laws/year/%d/", y), enKind, base)},
		Data: List{Total: total, Older: older, Newer: newer, Rows: list, Updated: s.pageUpdated(r, s.latestChange(ctx, cond, y, word), nil),
			Back: fmt.Sprintf("/laws/year/%d/", y), BackLabel: s.trOr(r, "All texts of "+label, "hub.year.back", label),
			Eyebrow: s.trOr(r, "Legislation · "+label, "years.legislation_of", label), Heading: title + "."}})
}

// i18nKindEn are the English names of the three words, for the English site's
// titles (the Arabic site reads hub.kind.<word> from ar.json).
var i18nKindEn = map[string]string{"law": "Laws", "decree": "Decrees", "text": "Decisions and other texts"}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return string(append([]rune{[]rune(s)[0] + ('a' - 'A')}, []rune(s)[1:]...))
}

// hubSitemapRows lists the hub pages for the core sitemap: every page of every
// code hub, and every page of every kind-and-year hub.
func (s *Server) hubSitemapRows(ctx context.Context) ([]SitemapURL, error) {
	var urls []SitemapURL
	rows, err := s.db.Query(ctx, `SELECT c.slug, (SELECT count(*) FROM legislation_article a WHERE a.law_id = c.law_id)::int
		FROM code c ORDER BY c.position`)
	if err != nil {
		return nil, err
	}
	type counted struct {
		key string
		n   int
	}
	codes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (counted, error) {
		var c counted
		return c, row.Scan(&c.key, &c.n)
	})
	if err != nil {
		return nil, err
	}
	for _, c := range codes {
		for p := 1; p <= max(1, (c.n+codeHubSize-1)/codeHubSize); p++ {
			urls = append(urls, SitemapURL{Path: codeHubPath(c.key, p)})
		}
	}
	rows, err = s.db.Query(ctx, `SELECT extract(year FROM l.issued_on)::int, `+kindWordSQL+`, count(*)::int
		FROM legislation l WHERE l.issued_on IS NOT NULL GROUP BY 1, 2 ORDER BY 1 DESC, 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var y, n int
		var word string
		if err := rows.Scan(&y, &word, &n); err != nil {
			return nil, err
		}
		urls = append(urls, SitemapURL{Path: kindYearPath(y, word)}) // later pages are cursor pages (keyset.go)
	}
	return urls, rows.Err()
}

// neighbours are the texts of l's year and kind word issued just before and
// just after it, in the order of its kind-and-year hub (newest first there), or
// nil at an end. Two index probes: a text then has a link to the text beside it
// as well as to the hub that lists it.
func (s *Server) neighbours(ctx context.Context, l *Law) (prev, next *LawRow, err error) {
	if l.IssuedOn == nil {
		return nil, nil, nil
	}
	y, word := l.IssuedOn.Year(), wordForKind(l.Kind)
	const year = `l.issued_on >= make_date($1, 1, 1) AND l.issued_on < make_date($1 + 1, 1, 1) AND ` + kindWordSQL + ` = $2`
	one := func(cmp, order string) (*LawRow, error) {
		rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
			WHERE `+year+` AND (l.issued_on, l.id) `+cmp+` ($3::date, $4) ORDER BY `+order+` LIMIT 1`, y, word, *l.IssuedOn, l.ID)
		if err != nil {
			return nil, err
		}
		rs, err := scanLawRows(rows)
		if err != nil || len(rs) == 0 {
			return nil, err
		}
		return &rs[0], nil
	}
	if prev, err = one("<", "l.issued_on DESC, l.id DESC"); err != nil {
		return nil, nil, err
	}
	next, err = one(">", "l.issued_on, l.id")
	return prev, next, err
}
