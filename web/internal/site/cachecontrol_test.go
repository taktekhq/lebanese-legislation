package site

import (
	"net/http/httptest"
	"testing"
)

func TestHTMLCacheControl(t *testing.T) {
	for _, c := range []struct {
		url  string
		code int
		want string
	}{
		{"/", 200, htmlCacheOK},
		{"/law/244226/", 200, htmlCacheOK},
		{"/ar/law/244226/", 200, htmlCacheOK},
		{"/laws/year/2020/?page=2", 200, htmlCacheOK},
		{"/search/?q=tax", 200, "no-store"},
		{"/ar/search/", 200, "no-store"},
		{"/ai/?notify=ok", 200, "no-store"},
		{"/law/1/", 404, htmlCache404},
		{"/law/1/", 500, "no-store"},
	} {
		if got := htmlCacheControl(httptest.NewRequest("GET", c.url, nil), c.code); got != c.want {
			t.Errorf("%s (%d): Cache-Control %q, want %q", c.url, c.code, got, c.want)
		}
	}
}
