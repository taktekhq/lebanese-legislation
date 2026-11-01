package site

import (
	"context"
	"encoding/xml"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJournalSitemapIsCompleteAndUsesPublicationDates(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/blog-sitemap.xml", nil))
	if w.Code != 200 {
		t.Fatalf("journal sitemap: %d", w.Code)
	}
	exported, err := JournalSitemapXML()
	if err != nil {
		t.Fatal(err)
	}
	if exported != w.Body.String() {
		t.Error("offline publication artifact differs from HTTP sitemap")
	}
	var data struct {
		URLs []struct {
			Loc     string `xml:"loc"`
			Lastmod string `xml:"lastmod"`
			Links   []struct {
				Lang string `xml:"hreflang,attr"`
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	posts := blogPosts(false)
	if len(data.URLs) != 2*(len(posts)+1) {
		t.Fatalf("got %d journal URLs", len(data.URLs))
	}
	expected := map[string]string{}
	latest := ""
	for _, p := range posts {
		for _, prefix := range []string{"", "/en"} {
			expected[Base+prefix+"/blog/"+p.Slug+"/"] = p.modified()
		}
		if p.modified() > latest {
			latest = p.modified()
		}
	}
	expected[Base+"/blog/"] = latest
	expected[Base+"/en/blog/"] = latest
	seen := map[string]bool{}
	for _, u := range data.URLs {
		if seen[u.Loc] {
			t.Errorf("duplicate %s", u.Loc)
		}
		seen[u.Loc] = true
		if want, ok := expected[u.Loc]; !ok || u.Lastmod != want {
			t.Errorf("%s lastmod %q want %q", u.Loc, u.Lastmod, want)
		}
		path := strings.TrimPrefix(strings.TrimPrefix(u.Loc, Base), "/en")
		wantLinks := map[string]string{"ar": Base + path, "en": Base + "/en" + path, "x-default": Base + path}
		if len(u.Links) != 3 {
			t.Errorf("%s needs three alternate links", u.Loc)
		}
		for _, link := range u.Links {
			if wantLinks[link.Lang] != link.Href {
				t.Errorf("bad alternate %+v", link)
			}
		}
	}
	robots := httptest.NewRecorder()
	s.ServeHTTP(robots, httptest.NewRequest("GET", "/robots.txt", nil))
	if !strings.Contains(robots.Body.String(), "Sitemap: "+Base+"/blog-sitemap.xml") {
		t.Error("robots must advertise journal sitemap")
	}
}

func TestAboutExplainsAndLinksTheJournal(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "ar"} {
		prefix := ""
		if lang == "en" {
			prefix = "/en"
		}
		r := httptest.NewRequest("GET", prefix+"/about/", nil)
		r = r.WithContext(context.WithValue(r.Context(), langCtxKey, lang))
		body, err := s.execute(r, "about", Page{Path: "/about/", Data: Stats{}})
		if err != nil {
			t.Fatal(err)
		}
		article := strings.SplitN(strings.SplitN(body, `<article class="prose">`, 2)[1], "</article>", 2)[0]
		if !strings.Contains(article, `href="`+prefix+`/blog/"`) {
			t.Errorf("%s About body lacks localized journal link", lang)
		}
	}
}
