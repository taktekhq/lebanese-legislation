package site

import (
	"encoding/xml"
	"net/http"
	"strings"
	"time"
)

// blogSitemap lists the journal independently of the much larger legal archive.
// Publication dates describe these pages; retrospective milestone dates do not.
func (s *Server) blogSitemap(w http.ResponseWriter, r *http.Request) {
	body, err := JournalSitemapXML()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	xmlOut(w, body)
}

// JournalSitemapXML exports the exact HTTP sitemap without a database or network.
// Publishers can retain this artifact alongside the verified deployment receipt.
func JournalSitemapXML() (string, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">` + "\n")
	for _, row := range blogSitemapRows() {
		for _, prefix := range []string{"", "/en"} {
			b.WriteString("<url><loc>")
			if err := xml.EscapeText(&b, []byte(Base+prefix+row.Path)); err != nil {
				return "", err
			}
			b.WriteString("</loc>")
			if stamp := lastmod(row.Mod); stamp != "" {
				b.WriteString("<lastmod>" + stamp + "</lastmod>")
			}
			for _, alt := range []struct{ lang, prefix string }{{"ar", ""}, {"en", "/en"}, {"x-default", ""}} {
				b.WriteString(`<xhtml:link rel="alternate" hreflang="` + alt.lang + `" href="`)
				if err := xml.EscapeText(&b, []byte(Base+alt.prefix+row.Path)); err != nil {
					return "", err
				}
				b.WriteString(`"/>`)
			}
			b.WriteString("</url>\n")
		}
	}
	b.WriteString("</urlset>\n")
	return b.String(), nil
}

func blogSitemapRows() []SitemapURL {
	rows := []SitemapURL{{Path: "/blog/"}}
	for _, post := range blogPosts(false) {
		stamp, _ := time.Parse("2006-01-02", post.modified())
		rows = append(rows, SitemapURL{Path: "/blog/" + post.Slug + "/", Mod: stamp})
		if stamp.After(rows[0].Mod) {
			rows[0].Mod = stamp
		}
	}
	return rows
}
