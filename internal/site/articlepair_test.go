package site

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestArticlePagesOfEveryKindCarryHreflang(t *testing.T) {
	s := noDBServer(t)
	for _, c := range []struct {
		law  *Law
		path string
	}{
		{&Law{LawRow: LawRow{ID: 198100, Kind: "مرسوم", Title: "مرسوم رقم 1"}}, "/decree/198100/article/3/"},
		{&Law{LawRow: LawRow{ID: 12, Kind: "قانون", Title: "قانون رقم 2"}}, "/law/12/article/3/"},
		{&Law{LawRow: LawRow{ID: 7, Kind: "قرار", Title: "قرار رقم 3"}}, "/text/7/article/3/"},
	} {
		c.law.List = []*Article{{Number: "2"}, {Number: "3"}, {Number: "4"}}
		for _, prefix := range []string{"", "/en"} {
			lang := map[string]string{"": "ar", "/en": "en"}[prefix]
			r := httptest.NewRequest("GET", prefix+c.path, nil)
			r = r.WithContext(context.WithValue(r.Context(), langCtxKey, lang))
			p := s.articlePage(r, c.law, 1)
			if p.Path != c.path {
				t.Fatalf("%s: page path %q", c.path, p.Path)
			}
			body, err := s.execute(r, "article", p)
			if err != nil {
				t.Fatalf("%s%s: %v", prefix, c.path, err)
			}
			canon, _, hl := head(body)
			if canon != Base+prefix+c.path || hl != 3 {
				t.Errorf("%s%s: canonical %q, %d hreflang links, want itself and 3", prefix, c.path, canon, hl)
			}
			assertHreflangPair(t, body, c.path, "/en"+c.path)
		}
	}
}
