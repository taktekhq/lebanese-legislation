package site

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicResourcesAreDirectAndBilingual(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"ar", "en"} {
		prefix := ""
		if lang == "en" {
			prefix = "/en"
		}
		r := httptest.NewRequest("GET", prefix+"/data/", nil)
		r = r.WithContext(context.WithValue(r.Context(), langCtxKey, lang))
		body, err := s.execute(r, "data", Page{Path: "/data/", Data: []TableInfo{}})
		if err != nil {
			t.Fatal(err)
		}
		for _, dest := range []string{"https://github.com/taktekhq/lebanese-legislation", "https://huggingface.co/datasets/taktekhq/lebanese-legislation/tree/main/releases", "https://huggingface.co/datasets/taktekhq/lebanese-legislation/tree/main/data", "https://huggingface.co/datasets/taktekhq/lebanese-court-scans", "https://huggingface.co/collections/taktekhq/lebanese-legislation", prefix + "/run-it-yourself/"} {
			if !strings.Contains(body, `href="`+dest+`"`) {
				t.Errorf("%s resource hub missing direct %s", lang, dest)
			}
		}
		if strings.Contains(body, "github.com/taktekhq/lebanese-legislation/releases") {
			t.Errorf("%s resource hub links a GitHub data release; the dump lives on Hugging Face", lang)
		}
		body, err = s.execute(r, "local", Page{Path: "/run-it-yourself/"})
		if err != nil {
			t.Fatal(err)
		}
		for _, image := range []string{"db", "api", "mcp", "web"} {
			if !strings.Contains(body, `href="https://github.com/orgs/taktekhq/packages/container/package/lebanese-legislation-`+image+`"`) {
				t.Errorf("%s run-it-yourself page misses image %s", lang, image)
			}
		}
		body, err = s.execute(r, "ai", Page{Path: "/ai/", Data: map[string]any{"Static": false}})
		if err != nil {
			t.Fatal(err)
		}
		for _, img := range []string{"claude", "chatgpt", "claude-code", "cursor"} {
			if !strings.Contains(body, `src="/assets/mcp/`+img+`.svg"`) {
				t.Errorf("%s MCP page misses its %s setup image", lang, img)
			}
		}
		if i, j := strings.Index(body, `href="`+prefix+`/run-it-yourself/"`), strings.Index(body, `id="notify"`); i < 0 || j < 0 || i > j {
			t.Errorf("%s MCP page does not point to run-it-yourself before the sign-up box", lang)
		}
		for _, id := range []string{"notify-ok", "notify-bad", "notify-limit"} {
			if !strings.Contains(body, `id="`+id+`"`) {
				t.Errorf("%s MCP page misses the #%s message the static sign-up redirects to", lang, id)
			}
		}
		if strings.Contains(body, "taktekbot@") || !strings.Contains(body, "nizar@taktek.io") {
			t.Errorf("%s MCP page must show nizar@taktek.io and no taktekbot address", lang)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", prefix+"/blog/open-source-local-service-starter/", nil))
		intro := strings.SplitN(w.Body.String(), `<section class="wrap blog-body">`, 2)[0]
		if !strings.Contains(intro, `href="https://github.com/taktekhq/lebanese-legislation"`) || !strings.Contains(intro, "GitHub") {
			t.Errorf("%s starter article lacks prominent explicit GitHub CTA", lang)
		}
	}
}

func TestEveryJournalResourceUsesPublicCurrentDestinations(t *testing.T) {
	for _, p := range blogPosts(false) {
		urls := []string{p.ActionPath, p.ResourceURL}
		for _, source := range p.Sources {
			urls = append(urls, source.URL)
		}
		for _, u := range urls {
			// The dump is published only on Hugging Face; GitHub releases are code versions.
			// The only GitHub repository a post links to is this public one.
			public := u == "https://github.com/taktekhq/lebanese-legislation" || strings.HasPrefix(u, "https://github.com/taktekhq/lebanese-legislation/") ||
				strings.HasPrefix(u, "https://github.com/taktekhq/lebanese-legislation#")
			if strings.Contains(u, "lebanese-legislation-starter") || strings.HasPrefix(u, "https://github.com/taktekhq/lebanese-legislation/releases") ||
				(strings.HasPrefix(u, "https://github.com/taktekhq/") && !public) {
				t.Errorf("%s links retired or private destination %s", p.Slug, u)
			}
		}
		if p.ResourceURL != "" && (p.ResourceLabel == "" || p.ResourceLabelAr == "") {
			t.Errorf("%s needs bilingual resource anchors", p.Slug)
		}
	}
}

func TestResourceUpdateKeepsHistoryAndAdvertisesActualChange(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "/en"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", prefix+"/blog/open-source-local-service-starter/", nil))
		body := w.Body.String()
		for _, want := range []string{`"datePublished":"2026-10-30"`, `"dateModified":"2026-10-31"`, `<time datetime="2026-10-30">2026-10-30</time>`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s article misses %s", prefix, want)
			}
		}
		if strings.Count(body, "<time ") != 1 {
			t.Errorf("%s article displays multiple dates", prefix)
		}
		feed := httptest.NewRecorder()
		s.ServeHTTP(feed, httptest.NewRequest("GET", prefix+"/blog/feed.xml", nil))
		if !strings.Contains(feed.Body.String(), "<updated>2026-10-31T00:00:00Z</updated>") {
			t.Errorf("%s feed misses update", prefix)
		}
	}
	xml, err := JournalSitemapXML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, `<loc>`+Base+`/en/blog/open-source-local-service-starter/</loc><lastmod>2026-10-31</lastmod>`) {
		t.Error("sitemap does not advertise the real resource update")
	}
}
