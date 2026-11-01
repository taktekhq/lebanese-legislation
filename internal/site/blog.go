package site

import (
	"context"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Blog dates distinguish the public accomplishment from this retrospective's publication.
//
//go:embed blog.json
var blogJSON []byte

type blogSection struct{ Heading, Body string }
type blogSource struct{ Label, LabelAr, URL string }
type blogPost struct {
	Updated                                                                                    string
	Slug, Milestone, Published, Commit, Title, TitleAr, Summary, SummaryAr, Action, ActionPath string
	EvidenceKind                                                                               string
	Sections, SectionsAr                                                                       []blogSection
	Sources                                                                                    []blogSource
	ResourceURL, ResourceLabel, ResourceLabelAr                                                string
}

func (p blogPost) modified() string {
	if p.Updated != "" {
		return p.Updated
	}
	return p.Published
}

func blogPosts(ar bool) []blogPost {
	var posts []blogPost
	if err := json.Unmarshal(blogJSON, &posts); err != nil {
		panic(err)
	}
	for i := range posts {
		for j := range posts[i].Sources {
			source := &posts[i].Sources[j]
			if ar && source.LabelAr != "" {
				source.Label = source.LabelAr
			}
			if !ar && strings.HasPrefix(source.URL, "/") && !strings.HasPrefix(source.URL, "//") && !strings.HasPrefix(source.URL, "/en/") {
				source.URL = "/en" + source.URL
			}
		}
		if ar {
			posts[i].ResourceLabel = posts[i].ResourceLabelAr
			posts[i].Title = posts[i].TitleAr
			posts[i].Summary = posts[i].SummaryAr
			posts[i].Sections = posts[i].SectionsAr
		}
	}
	sort.SliceStable(posts, func(i, j int) bool {
		if posts[i].Milestone != posts[j].Milestone {
			return posts[i].Milestone > posts[j].Milestone
		}
		if posts[i].Published != posts[j].Published {
			return posts[i].Published > posts[j].Published
		}
		return false
	})
	return posts
}
func (s *Server) blogRoutes() {
	s.mux.HandleFunc("GET /blog/{$}", s.blogIndex)
	s.mux.HandleFunc("GET /blog/{slug}/{$}", s.blogArticle)
	s.mux.HandleFunc("GET /blog/feed.xml", s.blogFeed)
	s.mux.HandleFunc("GET /blog-sitemap.xml", s.blogSitemap)
	s.AddCoreSitemap(func(context.Context) ([]SitemapURL, error) { return blogSitemapRows(), nil })
}
func (s *Server) blogIndex(w http.ResponseWriter, r *http.Request) {
	title, desc := "Project journal", "Changes to Lebanese Legislation, recorded retrospectively under the dates they happened. Read what changed and find the sources and interfaces you can use."
	if s.lang(r) == "ar" {
		title = "سجل المشروع"
		desc = "تغيّرات Lebanese Legislation موثّقة بأثر رجعي بحسب تواريخ حدوثها. اقرأ ما تغيّر، ولماذا يهم القارئ، وتعرّف إلى المصادر والواجهات المتاحة للاستخدام."
	}
	s.render(w, r, "blog", http.StatusOK, Page{Title: title + " · Lebanese Legislation", Desc: desc, Path: "/blog/", Data: blogPosts(s.lang(r) == "ar")})
}
func (s *Server) blogArticle(w http.ResponseWriter, r *http.Request) {
	for _, p := range blogPosts(s.lang(r) == "ar") {
		if p.Slug != r.PathValue("slug") {
			continue
		}
		path := "/blog/" + p.Slug + "/"
		citations := []string{Base + s.u(r, "/about/")}
		for _, source := range p.Sources {
			u := source.URL
			if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") {
				u = Base + u
			}
			citations = append(citations, u)
		}
		if len(p.Sources) == 0 {
			switch p.Slug {
			case "open-data-api-mcp":
				citations = append(citations, Base+"/ai/", "https://huggingface.co/datasets/taktekhq/lebanese-legislation", "https://github.com/taktekhq/lebanese-legislation")
			case "follow-amendments":
				citations = append(citations, Base+"/changes/feed.xml")
			case "english-titles-arabic-source":
				citations = append(citations, Base+"/en/laws/", "https://huggingface.co/datasets/taktekhq/lebanese-legislation")
			}
		}
		ld := map[string]any{"@context": "https://schema.org", "@type": "BlogPosting", "headline": p.Title, "description": p.Summary, "datePublished": p.Published, "dateModified": p.modified(), "inLanguage": s.lang(r), "url": Base + s.u(r, path), "mainEntityOfPage": Base + s.u(r, path), "image": Base + "/assets/og.png", "author": map[string]any{"@type": "Organization", "name": "Lebanese Legislation", "url": Base}, "publisher": map[string]any{"@type": "Organization", "name": "Taktek, LLC", "url": "https://taktek.io/"}, "citation": citations}
		s.render(w, r, "blogpost", http.StatusOK, Page{Title: p.Title + " · Lebanese Legislation", Desc: p.Summary, Path: path, OGType: "article", LD: []any{ld}, Data: p})
		return
	}
	s.notFound(w, r)
}
func (s *Server) blogFeed(w http.ResponseWriter, r *http.Request) {
	f := atomFeed{Title: "Lebanese Legislation project journal", ID: Base + s.u(r, "/blog/"), Author: atomAuthor{Name: "Lebanese Legislation"}, Links: []atomLink{{Href: Base + s.u(r, "/blog/feed.xml"), Rel: "self"}, {Href: Base + s.u(r, "/blog/")}}}
	for _, p := range blogPosts(s.lang(r) == "ar") {
		path := Base + s.u(r, "/blog/"+p.Slug+"/")
		if updated := p.modified() + "T00:00:00Z"; updated > f.Updated {
			f.Updated = updated
		}
		f.Entries = append(f.Entries, atomEntry{Title: p.Title, ID: path, Updated: p.modified() + "T00:00:00Z", Link: atomLink{Href: path}, Summary: p.Summary})
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=900")
	_, _ = fmt.Fprint(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(f)
}
