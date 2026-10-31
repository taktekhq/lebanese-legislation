package site

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Links   []atomLink  `xml:"link"`
	Author  atomAuthor  `xml:"author"`
	Entries []atomEntry `xml:"entry"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomEntry struct {
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Updated string   `xml:"updated"`
	Link    atomLink `xml:"link"`
	Summary string   `xml:"summary"`
}

func (s *Server) changesFeed(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	entries, err := s.latestChanges(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stamp := func(t *time.Time) string {
		if t == nil {
			return time.Unix(0, 0).UTC().Format(time.RFC3339)
		}
		return t.UTC().Format(time.RFC3339)
	}
	f := atomFeed{Title: "Changes to Lebanese law · Lebanese Legislation", ID: Base + "/changes/",
		Links:  []atomLink{{Href: Base + "/changes/feed.xml", Rel: "self"}, {Href: Base + "/changes/"}},
		Author: atomAuthor{Name: "Lebanese Legislation"}}
	for i, e := range entries {
		if i == 0 {
			f.Updated = stamp(e.Date)
		}
		var parts []string
		for _, t := range e.Targets {
			parts = append(parts, fmt.Sprintf("%s (%s)", t.Law.Ident(), t.Arts()))
		}
		f.Entries = append(f.Entries, atomEntry{Title: e.By.Ident(), ID: Base + lawPath(e.By.ID),
			Updated: stamp(e.Date), Link: atomLink{Href: Base + lawPath(e.By.ID)},
			Summary: "Changes " + strings.Join(parts, "; ")})
	}
	if f.Updated == "" {
		f.Updated = stamp(nil)
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=900")
	fmt.Fprint(w, xml.Header)
	enc := xml.NewEncoder(w)
	enc.Indent("", " ")
	if err := enc.Encode(f); err != nil {
		s.fail(w, r, err)
	}
}

func (s *Server) lawFeed(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	if s.rightWord(w, r, id) {
		return
	}
	l, err := s.loadLaw(ctx, id, true)
	if err != nil || l == nil {
		s.notFound(w, r)
		return
	}
	groups := l.LinkGroups()
	f := atomFeed{Title: "Changes to " + l.Ident() + " · Lebanese Legislation", ID: Base + lawPath(id),
		Links:  []atomLink{{Href: Base + lawPath(id) + "feed.xml", Rel: "self"}, {Href: Base + lawPath(id)}},
		Author: atomAuthor{Name: "Lebanese Legislation"}, Updated: time.Unix(0, 0).UTC().Format(time.RFC3339)}
	for i, g := range groups {
		up := time.Unix(0, 0).UTC().Format(time.RFC3339)
		if g.latest != nil {
			up = g.latest.UTC().Format(time.RFC3339)
		}
		if i == 0 {
			f.Updated = up
		}
		f.Entries = append(f.Entries, atomEntry{Title: g.By.Ident(), ID: fmt.Sprintf("%s%s#by-%d", Base, lawPath(id), g.By.ID),
			Updated: up, Link: atomLink{Href: Base + lawPath(g.By.ID)},
			Summary: "Changes " + l.Ident() + " (" + g.Arts() + ")"})
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	fmt.Fprint(w, xml.Header)
	enc := xml.NewEncoder(w)
	enc.Indent("", " ")
	if err := enc.Encode(f); err != nil {
		s.fail(w, r, err)
	}
}
