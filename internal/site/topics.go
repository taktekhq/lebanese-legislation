package site

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

// Topic pages: /topics/ lists them, /topic/<slug>/ explains one question in
// plain words, every statement tied to an article whose own words are quoted
// beneath it. The pages are the data files in internal/site/topics; the
// tests there keep each quote an exact piece of the article.
//
// The pages are written by a model (cmd/topicgen), so all of them, /topics/
// and every /topic/<slug>/ in both languages, are hidden from Google and out
// of the sitemaps (issue #86). Other engines may index them: Bing is the index
// ChatGPT and Copilot search, and they are where people ask these questions
// (Nizar, 3 Nov 2026: "hide them from google, but show them to Bing").

func (s *Server) topicRoutes() {
	s.mux.HandleFunc("GET /topics/{$}", s.topicsIndex)
	s.mux.HandleFunc("GET /topic/{slug}/{$}", s.topic)
}

// topicsHidden: the generated topic pages carry a googlebot noindex.
const topicsHidden = true

// TopicRef is a topic as the article page and the index name it.
type TopicRef struct{ Slug, Title, Question string }

// topicsFor lists, in the request's language, the topics that quote an article.
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
		Desc:  s.tr(r, "topics.desc"), Path: "/topics/", Data: list, GoogleNoIndex: topicsHidden,
		LD: []any{breadcrumbs(s.tr(r, "topics.title"), "/topics/")}})
}

// CiteView is one quoted article.
type CiteView struct {
	N      int    // the order of first use on the page
	Quote  string // Arabic, as printed
	Number string
	Label  string // how a Gazette reading is named (citeTarget); "" for an article
	Path   string // the article's address
	Law    string // how the law is cited, in Arabic
	LawEn  string // the code's English name, when it has one
	Hidden bool   // already quoted higher up the page
}

// citeTarget is where a cite points and, for a Gazette reading
// (topics.ParseReading), its label in the page's language: an annexed
// table's row is on the text's page, after the articles (annex.go), and an
// article's first wording under the article on its own page.
func (s *Server) citeTarget(ctx context.Context, r *http.Request, ct *topics.Cite) (path, label string) {
	rd, ok := topics.ParseReading(ct.Article)
	switch {
	case !ok:
		return artPath(ct.Law, ct.Article), ""
	case rd.Kind == "annex" && rd.Category == "p":
		return s.annexPath(ctx, r, ct.Law) + "#" + ct.Article, s.tr(r, "topic.annex_p", rd.Serial)
	case rd.Kind == "annex":
		return s.annexPath(ctx, r, ct.Law) + "#" + ct.Article, s.tr(r, "topic.annex", rd.Category, rd.Serial)
	}
	path = artPath(ct.Law, rd.Number) + "#" + ct.Article
	if y := s.readingYear(ctx, ct.Law, ct.Article); y != "" {
		return path, s.tr(r, "topic.original", rd.Number, y)
	}
	return path, s.tr(r, "topic.original_bare", rd.Number)
}

// readingNameAr is a Gazette reading's name in Arabic, for the page's
// schema.org citations, which name every article in Arabic.
func readingNameAr(article string) string {
	rd, _ := topics.ParseReading(article)
	switch {
	case rd.Kind == "annex" && rd.Category == "p":
		return arBundle.T("topic.annex_p", rd.Serial)
	case rd.Kind == "annex":
		return arBundle.T("topic.annex", rd.Category, rd.Serial)
	}
	return arBundle.T("topic.original_bare", rd.Number)
}

var annexPaths, readingYears sync.Map

// annexPath is the page a text's annexed tables are on: its own page, or the
// last part of a text long enough to be cut into parts (lawparts.go), where
// they follow the last article. Law 243/2012 is cut in two.
func (s *Server) annexPath(ctx context.Context, r *http.Request, id int) string {
	noteDep(r.Context(), "set:law") // the parts follow the law template's article sizes
	key := fmt.Sprintf("%d/%s", id, s.lang(r))
	if v, ok := annexPaths.Load(key); ok {
		return v.(string)
	}
	p := lawPath(id)
	if s.db == nil {
		return p
	}
	l, err := s.loadLaw(ctx, id, true)
	if err != nil {
		return p
	}
	runs, err := s.partsOf(r, l, 0)
	if err != nil {
		return p
	}
	if len(runs) > 1 {
		p = pagePartPath(id, len(runs))
	}
	annexPaths.Store(key, p)
	return p
}

// readingYear is the year a Gazette reading was published, "" when the
// database does not say (an older one has no gazette_annex).
func (s *Server) readingYear(ctx context.Context, law int, anchor string) string {
	key := fmt.Sprintf("%d/%s", law, anchor)
	if v, ok := readingYears.Load(key); ok {
		return v.(string)
	}
	if s.db == nil {
		return ""
	}
	var y int
	if s.db.QueryRow(ctx, `SELECT extract(year FROM gazette_date)::int FROM gazette_annex WHERE law_id = $1 AND anchor = $2`, law, anchor).Scan(&y) != nil {
		return ""
	}
	readingYears.Store(key, strconv.Itoa(y))
	return strconv.Itoa(y)
}

// ParaView is a paragraph with the articles it rests on.
type ParaView struct {
	Text   string
	Note   bool
	Refs   []CiteView // every article it cites, as links
	Quotes []CiteView // those not quoted yet, with their words
}

// RowView is a table row.
type RowView struct {
	Cells []string
	Refs  []CiteView
}

// TableView is a section's table; Quotes are the words of the articles its
// rows cite that the page has not quoted yet.
type TableView struct {
	Caption string
	Head    []string
	Rows    []RowView
	Quotes  []CiteView
}

// SectionView answers one sub-question.
type SectionView struct {
	Heading string
	ID      string
	Paras   []ParaView
	Table   *TableView
}

// addRef adds a link chip unless the block already links that article (two
// quotes of one article give one chip).
func addRef(refs []CiteView, v CiteView) []CiteView {
	for _, r := range refs {
		if r.Path == v.Path {
			return refs
		}
	}
	return append(refs, v)
}

// TopicView is what the topic template prints.
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

var topicLaws sync.Map // law id -> topicLaw

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
	return longDate(lang, t)
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
	order := map[string]int{}  // cite id -> number
	shown := map[string]bool{} // quoted already
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
		path, label := s.citeTarget(ctx, r, ct)
		v := CiteView{N: order[id], Quote: ct.Quote, Number: ct.Article, Label: label, Path: path, Law: law.cite, LawEn: law.en}
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
		Title: s.brandTitle(r, c.Title), Desc: c.Description, Path: path, OGType: "article", GoogleNoIndex: topicsHidden,
		LD:   []any{s.topicLD(r, t, c, path, ctx), breadcrumbs(s.tr(r, "topics.title"), "/topics/", c.Title, path)},
		Data: tv})
}

// topicLD is the page's schema.org Article: its modified date and a
// citation to the Legislation page of every article it quotes.
func (s *Server) topicLD(r *http.Request, t *topics.Topic, c *topics.Content, path string, ctx context.Context) map[string]any {
	var cites []any
	seen := map[string]bool{}
	for i := range t.Articles {
		ct := &t.Articles[i]
		p, label := s.citeTarget(ctx, r, ct)
		if seen[p] {
			continue
		}
		seen[p] = true
		name := "المادة " + ct.Article + " من " + s.topicLaw(ctx, ct.Law).cite
		if label != "" {
			name = readingNameAr(ct.Article) + " من " + s.topicLaw(ctx, ct.Law).cite
		}
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
