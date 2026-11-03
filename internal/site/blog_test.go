package site

import (
	"encoding/json"
	"encoding/xml"

	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestBlogBilingualDatesAndDiscovery(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "/en"} {
		for _, p := range blogPosts(false) {
			path := prefix + "/blog/" + p.Slug + "/"
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			body := w.Body.String()
			for _, want := range []string{`"datePublished":"` + p.Published + `"`, `"dateModified":"` + p.modified() + `"`, p.Milestone, `href="` + Base + path + `"`, `hreflang="en"`, `hreflang="ar"`} {
				if !strings.Contains(body, want) {
					t.Errorf("%s misses %s: status%d", path, want, w.Code)
				}
			}
			if w.Code != 200 {
				t.Errorf("%s = %d", path, w.Code)
			}
			if strings.Count(body, "<h1") != 1 {
				t.Errorf("%s needs one h1", path)
			}
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", prefix+"/blog/feed.xml", nil))
		if w.Code != 200 || strings.Count(w.Body.String(), "<entry>") != len(blogPosts(false)) {
			t.Errorf("feed %s: %d %s", prefix, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/blog/not-a-post/", nil))
	if w.Code != 404 {
		t.Errorf("missing slug: %d", w.Code)
	}
}
func TestBlogContentEvidence(t *testing.T) {
	var posts []blogPost
	if err := json.Unmarshal(blogJSON, &posts); err != nil {
		t.Fatal(err)
	}
	if len(posts) < 4 {
		t.Fatal("need several milestone posts")
	}
	for _, p := range posts {
		if len(p.Sections) < 4 || len(p.SectionsAr) != len(p.Sections) || p.TitleAr == "" {
			t.Errorf("incomplete %s", p.Slug)
		}
		if p.Commit == "" && p.EvidenceKind != "conversation" && p.EvidenceKind != "publication-record" {
			t.Errorf("missing primary history evidence %s", p.Slug)
		}
		if p.EvidenceKind != "" && p.EvidenceKind != "conversation" && p.EvidenceKind != "publication-record" && p.EvidenceKind != "public-commit" {
			t.Errorf("unknown primary evidence kind %s: %s", p.Slug, p.EvidenceKind)
		}
		if p.EvidenceKind == "publication-record" && len(p.Sources) == 0 {
			t.Errorf("missing public publication record source %s", p.Slug)
		}
		if p.Published < p.Milestone {
			t.Errorf("future milestone %s", p.Slug)
		}
	}
}

// New retrospective entries belong at the date they describe, even when they
// were written later. Equal dates retain their editorial order in the file.
func TestBlogNewestMilestoneFirst(t *testing.T) {
	original := blogJSON
	t.Cleanup(func() { blogJSON = original })
	blogJSON = []byte(`[
		{"Slug":"old-newly-written","Milestone":"2026-10-08","Published":"2026-11-03"},
		{"Slug":"same-z","Milestone":"2026-10-21","Published":"2026-10-30"},
		{"Slug":"same-a","Milestone":"2026-10-21","Published":"2026-10-30"},
		{"Slug":"newest","Milestone":"2026-10-29","Published":"2026-10-30"},
		{"Slug":"same-later-publication","Milestone":"2026-10-21","Published":"2026-10-31"}
	]`)
	want := []string{"newest", "same-later-publication", "same-z", "same-a", "old-newly-written"}
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "/en"} {
		posts := blogPosts(prefix == "")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", prefix+"/blog/", nil))
		last := -1
		for i, slug := range want {
			if posts[i].Slug != slug {
				t.Errorf("%s position %d: got %s, want %s", prefix, i, posts[i].Slug, slug)
			}
			pos := strings.Index(w.Body.String(), `href="`+prefix+"/blog/"+slug+`/"`)
			if pos <= last {
				t.Errorf("%s index: %s missing or out of order", prefix, slug)
			}
			last = pos
		}
		w = httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", prefix+"/blog/feed.xml", nil))
		var feed atomFeed
		if err := xml.Unmarshal(w.Body.Bytes(), &feed); err != nil {
			t.Fatal(err)
		}
		if len(feed.Entries) != len(want) {
			t.Fatalf("%s feed has %d entries", prefix, len(feed.Entries))
		}
		for i, entry := range feed.Entries {
			if entry.ID != Base+prefix+"/blog/"+want[i]+"/" || entry.Updated != posts[i].modified()+"T00:00:00Z" {
				t.Errorf("%s feed position %d: wrong URL or actual publication date: %+v", prefix, i, entry)
			}
		}
	}
}

func blogElements(root *html.Node, name string) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == name {
			out = append(out, n)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return out
}

func blogAttribute(n *html.Node, name string) string {
	for _, attr := range n.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func blogText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var out strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		out.WriteString(blogText(child))
	}
	return out.String()
}

func TestBlogSingleVisibleDateAndFooterGroups(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	posts := blogPosts(false)
	for _, prefix := range []string{"", "/en"} {
		paths := []string{prefix + "/blog/"}
		for _, p := range posts {
			paths = append(paths, prefix+"/blog/"+p.Slug+"/")
		}
		for i, path := range paths {
			t.Run(path, func(t *testing.T) {
				w := httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				if w.Code != 200 {
					t.Fatalf("status %d", w.Code)
				}
				root, err := html.Parse(w.Body)
				if err != nil {
					t.Fatal(err)
				}
				main := blogElements(root, "main")[0]
				if i == 0 {
					notice := "recorded retrospectively"
					if prefix == "" {
						notice = "موثّقة بأثر رجعي"
					}
					if !strings.Contains(blogText(main), notice) {
						t.Error("journal introduction must explain its historical dates")
					}
				}
				dates := blogElements(main, "time")
				expected := posts
				if i > 0 {
					expected = posts[i-1 : i]
				}
				if len(dates) != len(expected) {
					t.Fatalf("got %d visible dates, want %d", len(dates), len(expected))
				}
				for j, date := range dates {
					if blogText(date) != expected[j].Milestone || blogAttribute(date, "datetime") != expected[j].Milestone {
						t.Errorf("visible date %q, want journal date %s", blogText(date), expected[j].Milestone)
					}
				}
				for _, label := range []string{"Milestone:", "Article published", "تاريخ الإنجاز:", "نُشرت المقالة", "نُشرت وحُدّثت المقالة"} {
					if strings.Contains(blogText(blogElements(main, "section")[0]), label) {
						t.Errorf("redundant date label %q", label)
					}
				}
				footer := blogElements(root, "footer")[0]
				groups := map[string]*html.Node{}
				for _, h := range blogElements(footer, "h2") {
					groups[blogText(h)] = h.Parent
				}
				use, info := "Use it", "Information"
				if prefix == "" {
					use, info = "الاستخدام", "معلومات"
				}
				if groups[use] == nil || groups[info] == nil {
					t.Fatalf("missing translated footer groups: %v", groups)
				}
				for _, label := range []string{"Project journal", "سجل المشروع", "About", "Analytics settings", "حول", "إعدادات الإحصاءات"} {
					if strings.Contains(blogText(groups[use]), label) {
						t.Errorf("%q belongs outside %s", label, use)
					}
				}
				buttons := blogElements(groups[info], "button")
				if len(buttons) != 0 {
					t.Error("information group must not contain analytics preference controls")
				}
				links := blogElements(groups[info], "a")
				if len(links) != 6 || blogAttribute(links[0], "href") != prefix+"/blog/" || blogAttribute(links[1], "href") != prefix+"/about/" || blogAttribute(links[2], "href") != "https://github.com/taktekhq/lebanese-legislation" || blogAttribute(links[3], "href") != "https://huggingface.co/taktekhq" || blogAttribute(links[4], "href") != prefix+"/privacy/" || blogAttribute(links[5], "href") != prefix+"/terms/" {
					t.Error("information group needs the localized journal, About, privacy and terms links")
				}
			})
		}
	}
}

func TestBlogArticlePublicSources(t *testing.T) {
	original := blogJSON
	t.Cleanup(func() { blogJSON = original })
	fixture := blogPost{
		Slug: "new-historical-entry", Title: "Historical entry", TitleAr: "مقالة تاريخية",
		Milestone: "2026-10-21", Published: "2026-10-30", Commit: "private-evidence-commit",
		Sources: []blogSource{
			{Label: "Public interface", LabelAr: "الواجهة العامة", URL: "/data/"},
			{Label: "Public dataset", LabelAr: "مجموعة البيانات العامة", URL: "https://huggingface.co/datasets/taktekhq/lebanese-legislation"},
			{Label: "English interface", URL: "/en/laws/"},
		},
	}
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	// A source list works for any new slug and replaces legacy slug-specific
	// sources. Entries without the field still use their existing sources.
	for _, slug := range []string{"new-historical-entry", "open-data-api-mcp"} {
		fixture.Slug = slug
		blogJSON, err = json.Marshal([]blogPost{fixture})
		if err != nil {
			t.Fatal(err)
		}
		for _, prefix := range []string{"", "/en"} {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", prefix+"/blog/"+slug+"/", nil))
			if w.Code != 200 {
				t.Fatalf("%s%s status %d", prefix, slug, w.Code)
			}
			body := w.Body.String()
			root, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			links := map[string]string{}
			for _, a := range blogElements(blogElements(root, "article")[0], "a") {
				links[blogAttribute(a, "href")] = blogText(a)
			}
			label := "Public interface"
			if prefix == "" {
				label = "الواجهة العامة"
			}
			if links[prefix+"/data/"] != label || links[fixture.Sources[1].URL] == "" || links["/en/laws/"] != "English interface" {
				t.Errorf("%s%s: missing translated public sources: %v", prefix, slug, links)
			}
			var articleLD struct {
				Type     string   `json:"@type"`
				Citation []string `json:"citation"`
			}
			for _, script := range blogElements(root, "script") {
				if blogAttribute(script, "type") != "application/ld+json" {
					continue
				}
				if err := json.Unmarshal([]byte(blogText(script)), &articleLD); err != nil {
					t.Fatal(err)
				}
				if articleLD.Type == "BlogPosting" {
					break
				}
			}
			want := []string{Base + prefix + "/about/", Base + prefix + "/data/", fixture.Sources[1].URL, Base + "/en/laws/"}
			if strings.Join(articleLD.Citation, "\n") != strings.Join(want, "\n") {
				t.Errorf("%s%s citations: got %v, want %v", prefix, slug, articleLD.Citation, want)
			}
			if strings.Contains(body, fixture.Commit) {
				t.Errorf("%s%s rendered internal evidence", prefix, slug)
			}
			// Global navigation may link the public repository. It must not be
			// invented as this fixture's article-specific source or citation.
			if links["https://github.com/taktekhq/lebanese-legislation"] != "" {
				t.Errorf("%s%s rendered a legacy source in the article", prefix, slug)
			}
		}
	}
	fixture.Sources = nil
	blogJSON, err = json.Marshal([]blogPost{fixture})
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "/en"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", prefix+"/blog/open-data-api-mcp/", nil))
		if !strings.Contains(w.Body.String(), `href="https://github.com/taktekhq/lebanese-legislation"`) {
			t.Errorf("%s: missing legacy source fallback", prefix)
		}
	}

}
