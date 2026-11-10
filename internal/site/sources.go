package site

import (
	"net/http"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
)

// Two pages about where Lebanese law is published online.
//
// /sources/ answers "where can I read Lebanese law online" with one table of
// the places that publish it. Every cell about another site was read on that
// site on sourcesChecked; a cell with no string in en.json prints "not
// stated" (sources.not_stated), never a guess. To change a row, read the
// source's own site again, edit its sources.<key>.* strings and move the date.
//
// /legiliban/ says what Legiliban is (the Lebanese University's legal
// database, the main source of the texts here) and what this site is: an
// independent project, not affiliated with the university.

// sourcesChecked is the day the table's facts were last read on the sources'
// own sites; legilibanUpdated is the day the Legiliban page last changed.
// They are the visible dates, the JSON-LD dateModified and the sitemap lastmod.
var (
	sourcesChecked   = time.Date(2026, time.November, 10, 0, 0, 0, 0, time.UTC)
	legilibanUpdated = time.Date(2026, time.November, 10, 0, 0, 0, 0, time.UTC)
)

// infoPageMods are the dates the core sitemap gives these two pages.
var infoPageMods = map[string]time.Time{"/sources/": sourcesChecked, "/legiliban/": legilibanUpdated}

// sourceLink is one address of a source: where it leads and how it is shown.
type sourceLink struct{ URL, Label string }

// sourceRows are the table's rows, in the order they are printed: the public
// bodies first, then the commercial databases, the specialised collections
// and this site last. The order is not a ranking.
var sourceRows = []struct {
	key   string
	links []sourceLink
}{
	{"legiliban", []sourceLink{{"https://legallaw.ul.edu.lb/", "legallaw.ul.edu.lb"}}},
	{"gazette", []sourceLink{{"http://www.pcm.gov.lb/", "pcm.gov.lb"}}},
	{"gazette_digital", []sourceLink{{"https://jo.pcm.gov.lb/", "jo.pcm.gov.lb"}}},
	{"parliament", []sourceLink{{"https://www.lp.gov.lb/", "lp.gov.lb"}}},
	{"council", []sourceLink{{"https://www.cc.gov.lb/", "cc.gov.lb"}}},
	{"mustachar", []sourceLink{{"https://almustachar.com/", "almustachar.com"}}},
	{"sader", []sourceLink{{"https://www.saderlex.com/", "saderlex.com"}}},
	{"hamorabi", []sourceLink{{"https://hamorabi.com/", "hamorabi.com"}}},
	{"legal_agenda", []sourceLink{{"https://legal-agenda.com/", "legal-agenda.com"}, {"https://english.legal-agenda.com/", "english.legal-agenda.com"}}},
	{"natlex", []sourceLink{{"https://natlex.ilo.org/", "natlex.ilo.org"}}},
	{"wipo", []sourceLink{{"https://www.wipo.int/wipolex/en/members/profile/LB", "wipo.int/wipolex"}}},
	{"constitute", []sourceLink{{"https://www.constituteproject.org/constitution/Lebanon_2004", "constituteproject.org"}}},
	{"self", nil},
}

// sourceCols are the table's columns after the source's name.
var sourceCols = []string{"price", "lang", "holds", "fulltext", "search", "api"}

// SourceView is one row as the template prints it.
type SourceView struct {
	Name  string
	Links []sourceLink
	Cells []string
}

// SourcesView is what the sources template prints.
type SourcesView struct {
	Head    []string
	Rows    []SourceView
	Updated string // the day the facts were checked, in the page's language
	ISO     string
	Count   int
}

func (s *Server) sources(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	st, err := s.stats(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	lang := s.lang(r)
	v := SourcesView{Updated: longDate(lang, sourcesChecked), ISO: sourcesChecked.Format("2006-01-02"), Count: len(sourceRows)}
	for _, c := range sourceCols {
		v.Head = append(v.Head, s.tr(r, "sources.col."+c))
	}
	path := "/sources/"
	var items []any
	for i, row := range sourceRows {
		sv := SourceView{Name: s.tr(r, "sources."+row.key+".name"), Links: row.links}
		for _, c := range sourceCols {
			key := "sources." + row.key + "." + c
			switch {
			case key == "sources.self.holds":
				sv.Cells = append(sv.Cells, s.tr(r, key, thousands(st.Laws), thousands(st.Rulings), thousands(st.Gazette)))
			case key == "sources.self.fulltext":
				sv.Cells = append(sv.Cells, s.tr(r, key, thousands(st.WithText), thousands(st.Articles)))
			case i18n.English[key] == "": // the source's site did not say
				sv.Cells = append(sv.Cells, s.tr(r, "sources.not_stated"))
			default:
				sv.Cells = append(sv.Cells, s.tr(r, key))
			}
		}
		url := Base + "/"
		if len(row.links) > 0 {
			url = row.links[0].URL
		} else {
			sv.Links = []sourceLink{{s.u(r, "/"), "lebaneselegislation.com"}}
		}
		v.Rows = append(v.Rows, sv)
		items = append(items, map[string]any{"@type": "ListItem", "position": i + 1, "name": sv.Name, "url": url})
	}
	title := s.tr(r, "sources.title")
	page := Base + s.u(r, path)
	s.render(w, r, "sources", http.StatusOK, Page{
		Title: s.brandTitle(r, title), Desc: s.tr(r, "sources.desc"), Path: path,
		LD: []any{
			map[string]any{"@context": "https://schema.org", "@type": "ItemList", "@id": page + "#sources", "url": page,
				"name": title, "description": s.tr(r, "sources.desc"), "numberOfItems": len(items),
				"dateModified": v.ISO, "itemListElement": items},
			map[string]any{"@context": "https://schema.org", "@type": "WebPage", "@id": page, "url": page, "name": title,
				"inLanguage": lang, "dateModified": v.ISO, "mainEntity": map[string]any{"@id": page + "#sources"},
				"publisher": map[string]any{"@type": "Organization", "name": "Taktek, LLC", "url": "https://taktek.io/"}},
			breadcrumbs(title, path)},
		Data: v})
}

// LegilibanView is what the Legiliban template prints.
type LegilibanView struct {
	Stats
	Updated string
	ISO     string
}

func (s *Server) legiliban(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	st, err := s.stats(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	lang := s.lang(r)
	path := "/legiliban/"
	title := s.tr(r, "legiliban.title")
	page := Base + s.u(r, path)
	iso := legilibanUpdated.Format("2006-01-02")
	centre := map[string]any{"@type": "Organization", "name": "Centre for Research and Studies in Legal Informatics, Lebanese University",
		"alternateName": "مركز الدراسات والأبحاث في المعلوماتية القانونية", "url": "https://legallaw.ul.edu.lb/"}
	s.render(w, r, "legiliban", http.StatusOK, Page{
		Title: s.brandTitle(r, title), Desc: s.tr(r, "legiliban.desc"), Path: path,
		LD: []any{
			map[string]any{"@context": "https://schema.org", "@type": "WebPage", "@id": page, "url": page, "name": title,
				"description": s.tr(r, "legiliban.desc"), "inLanguage": lang, "dateModified": iso,
				"about": map[string]any{"@type": "WebSite", "name": "Legiliban", "alternateName": "legiliban.ul.edu.lb",
					"url": "https://legallaw.ul.edu.lb/", "publisher": centre},
				"publisher": map[string]any{"@type": "Organization", "name": "Taktek, LLC", "url": "https://taktek.io/"}},
			breadcrumbs(title, path)},
		Data: LegilibanView{Stats: st, Updated: longDate(lang, legilibanUpdated), ISO: iso}})
}
