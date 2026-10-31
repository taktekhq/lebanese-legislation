package site

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/taktekhq/lebanese-legislation/web/internal/site/topics"
)

func (s *Server) topicRoutes() {
	s.mux.HandleFunc("GET /topics/{$}", s.topicsIndex)
	s.mux.HandleFunc("GET /topic/{slug}/{$}", s.topic)
	s.AddCoreSitemap(func(context.Context) ([]SitemapURL, error) {
		var urls []SitemapURL
		for _, p := range topicSitemapPaths() {
			urls = append(urls, SitemapURL{Path: p})
		}
		return urls, nil
	})
}

func topicSitemapPaths() []string {
	paths := []string{"/topics/"}
	for _, t := range topics.All() {
		paths = append(paths, "/topic/"+t.Slug+"/")
	}
	return paths
}

type TopicRef struct{ Slug, Title, Question string }

func (s *Server) topicsFor(r *http.Request, law int, number string) []TopicRef {
	var out []TopicRef
	for _, t := range topics.ByArticle(law, number) {
		c := t.Lang(s.lang(r))
		out = append(out, TopicRef{t.Slug, c.Title, c.Question})
	}
	return out
}

func (s *Server) topicsIndex(w http.ResponseWriter, r *http.Request) {
	var list []TopicRef
	for _, t := range topics.All() {
		c := t.Lang(s.lang(r))
		list = append(list, TopicRef{t.Slug, c.Title, c.Question})
	}
	s.render(w, r, "topics", 200, Page{
		Title: s.brandTitle(r, s.tr(r, "topics.title")),
		Desc:  s.tr(r, "topics.desc"), Path: "/topics/", Data: list,
		LD: []any{breadcrumbs(s.tr(r, "topics.title"), "/topics/")}})
}

type CiteView struct {
	N      int
	Quote  string
	Number string
	Path   string
	Law    string
	LawEn  string
	Hidden bool
}

type ParaView struct {
	Text   string
	Note   bool
	Refs   []CiteView
	Quotes []CiteView
}

type RowView struct {
	Cells []string
	Refs  []CiteView
}

type TableView struct {
	Caption string
	Head    []string
	Rows    []RowView
	Quotes  []CiteView
}

type SectionView struct {
	Heading string
	ID      string
	Paras   []ParaView
	Table   *TableView
}

func addRef(refs []CiteView, v CiteView) []CiteView {
	for _, r := range refs {
		if r.Path == v.Path {
			return refs
		}
	}
	return append(refs, v)
}

type TopicView struct {
	Slug     string
	C        *topics.Content
	Answer   ParaView
	Sections []SectionView
	Updated  string
	Other    []TopicRef
}

type topicLaw struct {
	cite, en string
}

var topicLaws sync.Map

func (s *Server) topicLaw(ctx context.Context, id int) topicLaw {
	if v, ok := topicLaws.Load(id); ok {
		return v.(topicLaw)
	}
	if s.db == nil {
		return topicLaw{cite: fmt.Sprintf("#%d", id)}
	}
	var x LawRow
	err := s.db.QueryRow(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind WHERE l.id = $1`, id).Scan(lawRowDest(&x)...)
	if err != nil {
		return topicLaw{cite: fmt.Sprintf("#%d", id)}
	}
	l := topicLaw{cite: x.CiteName()}
	if x.Code != nil {
		l.en = *x.Code
	}
	topicLaws.Store(id, l)
	return l
}

var enMonths = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
var arMonths = []string{"كانون الثاني", "شباط", "آذار", "نيسان", "أيار", "حزيران", "تموز", "آب", "أيلول", "تشرين الأول", "تشرين الثاني", "كانون الأول"}

func topicDate(lang, iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	if lang == "ar" {
		return fmt.Sprintf("%d %s %d", t.Day(), arMonths[t.Month()-1], t.Year())
	}
	return fmt.Sprintf("%d %s %d", t.Day(), enMonths[t.Month()-1], t.Year())
}

func (s *Server) topic(w http.ResponseWriter, r *http.Request) {
	t := topics.Get(r.PathValue("slug"))
	if t == nil {
		s.notFound(w, r)
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	lang := s.lang(r)
	c := t.Lang(lang)
	n := 0
	order := map[string]int{}
	shown := map[string]bool{}
	view := func(id string, quote bool) (CiteView, bool) {
		ct := t.Cite(id)
		if ct == nil {
			return CiteView{}, false
		}
		if _, ok := order[id]; !ok {
			n++
			order[id] = n
		}
		law := s.topicLaw(ctx, ct.Law)
		v := CiteView{N: order[id], Quote: ct.Quote, Number: ct.Article, Path: artPath(ct.Law, ct.Article), Law: law.cite, LawEn: law.en}
		first := !shown[id]
		if quote {
			shown[id] = true
		}
		return v, first
	}
	para := func(p topics.Para) ParaView {
		pv := ParaView{Text: p.Text, Note: p.Note}
		for _, id := range p.Cites {
			v, first := view(id, true)
			pv.Refs = addRef(pv.Refs, v)
			if first {
				pv.Quotes = append(pv.Quotes, v)
			}
		}
		return pv
	}
	tv := TopicView{Slug: t.Slug, C: c, Updated: topicDate(lang, t.Updated)}
	tv.Answer = para(c.Answer)
	for i, sec := range c.Sections {
		sv := SectionView{Heading: sec.Heading, ID: fmt.Sprintf("s%d", i+1)}
		for _, p := range sec.Paras {
			sv.Paras = append(sv.Paras, para(p))
		}
		if sec.Table != nil {
			tb := &TableView{Caption: sec.Table.Caption, Head: sec.Table.Head}
			for _, row := range sec.Table.Rows {
				rv := RowView{Cells: row.Cells}
				for _, id := range row.Cites {
					v, first := view(id, true)
					rv.Refs = addRef(rv.Refs, v)
					if first {
						tb.Quotes = append(tb.Quotes, v)
					}
				}
				tb.Rows = append(tb.Rows, rv)
			}
			sv.Table = tb
		}
		tv.Sections = append(tv.Sections, sv)
	}
	for _, o := range topics.All() {
		if o.Slug != t.Slug {
			oc := o.Lang(lang)
			tv.Other = append(tv.Other, TopicRef{o.Slug, oc.Title, oc.Question})
		}
	}
	path := "/topic/" + t.Slug + "/"
	s.render(w, r, "topic", 200, Page{
		Title: s.brandTitle(r, c.Title), Desc: c.Description, Path: path, OGType: "article",
		LD:   []any{s.topicLD(r, t, c, path, ctx), breadcrumbs(s.tr(r, "topics.title"), "/topics/", c.Title, path)},
		Data: tv})
}

func (s *Server) topicLD(r *http.Request, t *topics.Topic, c *topics.Content, path string, ctx context.Context) map[string]any {
	var cites []any
	seen := map[string]bool{}
	for _, ct := range t.Articles {
		p := artPath(ct.Law, ct.Article)
		if seen[p] {
			continue
		}
		seen[p] = true
		name := "المادة " + ct.Article + " من " + s.topicLaw(ctx, ct.Law).cite
		cites = append(cites, map[string]any{"@type": "Legislation", "@id": Base + p, "url": Base + p, "name": name,
			"inLanguage": "ar", "isPartOf": map[string]any{"@type": "Legislation", "@id": Base + lawPath(ct.Law)}})
	}
	org := map[string]any{"@type": "Organization", "name": "Lebanese Legislation", "url": Base + "/"}
	url := Base + s.u(r, path)
	return map[string]any{"@context": "https://schema.org", "@type": "Article", "@id": url + "#article", "url": url,
		"mainEntityOfPage": url, "headline": c.Question, "name": c.Title, "description": c.Description,
		"inLanguage": s.lang(r), "datePublished": t.Published, "dateModified": t.Updated,
		"author": org, "publisher": org, "isAccessibleForFree": true, "citation": cites}
}
