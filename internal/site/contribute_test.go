package site

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContributeRoutes(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, title, lang string }{
		{"/contribute/", "ساهم", "ar"},
		{"/en/contribute/", "Contribute", "en"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
			body := rec.Body.String()
			if rec.Code != 200 {
				t.Fatalf("status %d", rec.Code)
			}
			for _, want := range []string{
				`<html lang="` + tc.lang + `"`,
				`<h1 class="page__title">` + tc.title + `</h1>`,
				`href="https://github.com/taktekhq/lebanese-legislation"`,
				`href="https://huggingface.co/spaces/taktekhq/lebanese-legal-handwriting"`,
				`href="https://huggingface.co/collections/taktekhq/lebanese-legislation"`,
				`href="https://github.com/taktekhq/lebanese-legislation"`,
				`hreflang="en"`, `hreflang="ar"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			// The page itself collects nothing; the only form is the footer's
			// "report an error" note every page has (Nizar, 5 Nov 2026).
			if i := strings.Index(body, `<form id="report-form"`); i >= 0 {
				body = body[:i] + body[i+strings.Index(body[i:], `</form>`)+len(`</form>`):]
			}
			if strings.Contains(body, `<form`) {
				t.Error("contribution page must not collect submissions")
			}
		})
	}
}

// Every page's footer offers "report an error" in the page's language: a
// note sent to PostHog, shown only with JavaScript (Nizar, 5 Nov 2026).
func TestFooterReportForm(t *testing.T) {
	s, err := newServer(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for path, open := range map[string]string{"/contribute/": "أبلغ عن خطأ في هذه الصفحة", "/en/contribute/": "Report an error on this page"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		body := rec.Body.String()
		for _, want := range []string{`data-report-open aria-expanded="false" aria-controls="report-form">` + open + `</button>`, `<form id="report-form"`, `data-report-form hidden`, `<textarea id="report-note"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", path, want)
			}
		}
	}
}

// The page has its own share image per language, named before the
// site-wide og.png: link previews take the first og:image.
func TestContributeShareImage(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for path, lang := range map[string]string{"/contribute/": "ar", "/en/contribute/": "en"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		body := rec.Body.String()
		own := `<meta property="og:image" content="` + Base + `/assets/og-contribute-` + lang + `.png">`
		i, j := strings.Index(body, own), strings.Index(body, `<meta property="og:image" content="`+Base+`/assets/og.png">`)
		if i < 0 || j < 0 || i > j {
			t.Errorf("%s: own og:image at %d, site-wide at %d; want own first", path, i, j)
		}
		if !strings.Contains(body, `<meta name="twitter:image" content="`+Base+`/assets/og-contribute-`+lang+`.png">`) {
			t.Errorf("%s: no twitter:image", path)
		}
		if strings.Contains(body, `og:image:alt" content="">`) {
			t.Errorf("%s: empty image alt", path)
		}
		img := httptest.NewRecorder()
		s.ServeHTTP(img, httptest.NewRequest("GET", "/assets/og-contribute-"+lang+".png", nil))
		if img.Code != 200 || !strings.HasPrefix(img.Body.String(), "\x89PNG") {
			t.Errorf("%s: image answers %d", path, img.Code)
		}
	}
}
