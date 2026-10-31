package site

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCouncilRulingRenders(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	on := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	text := "نص"
	law := &Law{LawRow: LawRow{ID: 2000200481, Kind: "قانون", Title: "منح عفو عام", Status: "in_force"}}
	for _, n := range []string{"1", "2", "3"} {
		a := &Article{Number: n, Versions: []Version{{Major: 1, State: "in_force", Source: "centre", TextAr: &text}}}
		law.List = append(law.List, a)
		law.Items = append(law.Items, Item{Article: a})
	}
	src := "https://www.cc.gov.lb/documents/598/x.pdf"
	law.attachCouncil([]CouncilRuling{
		{Effect: "annulled_in_part", Decision: "12/2026", On: &on, SourceURL: src, En: "whole en", Ar: "whole ar"},
		{Number: "2", Effect: "annulled_in_part", Decision: "12/2026", On: &on, SourceURL: src, En: "two en", Ar: "two ar"},
		{Number: "3", Effect: "annulled_in_part", Decision: "12/2026", On: &on, SourceURL: src, En: "three en", Ar: "three ar"},
	})
	if len(law.Council) != 1 || len(law.Council[0].Articles) != 2 || len(law.List[0].Council) != 0 || len(law.List[1].Council) != 1 {
		t.Fatalf("grouping: %+v", law.Council)
	}
	if law.Annulled() || law.List[1].AnnulledWhole() {
		t.Error("annulled in part reads as annulled")
	}
	for _, c := range []struct{ lang, path, want string }{
		{"en", "/en/law/2000200481/", "Annulled in part"},
		{"ar", "/law/2000200481/", "إبطال جزئي"},
	} {
		r := httptest.NewRequest("GET", c.path, nil)
		r = r.WithContext(context.WithValue(r.Context(), langCtxKey, c.lang))
		out, err := s.execute(r, "law", Page{Title: "x", Path: "/law/2000200481/", Lang: "ar", Data: law})
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		for _, w := range []string{c.want, "12/2026", src, "/article/2/", "/article/3/"} {
			if !strings.Contains(out, w) {
				t.Errorf("%s: no %q", c.path, w)
			}
		}
		if n := strings.Count(out, `href="`+src+`"`); n != 3 {
			t.Errorf("%s: %d source links, want 3 (text, article 2, article 3)", c.path, n)
		}
	}
}

func TestCouncilNotInForceRenders(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	on := time.Date(2025, 5, 20, 0, 0, 0, 0, time.UTC)
	law := &Law{LawRow: LawRow{ID: 333138, Kind: "قانون نافذ حكما", Title: "الإيجارات للأماكن غير السكنية", Status: "in_force"}}
	src := "http://www.pcm.gov.lb/archive/j2025/j23/wfk/kri5-2025.htm"
	law.attachCouncil([]CouncilRuling{{Effect: "not_in_force", Decision: "5/2025", On: &on, SourceURL: src, En: "whole en", Ar: "whole ar"}})
	if !law.NotInForce() || law.Annulled() {
		t.Fatalf("NotInForce %v, Annulled %v", law.NotInForce(), law.Annulled())
	}
	for _, c := range []struct{ lang, path, want string }{
		{"en", "/en/law/333138/", "Declared not in force by the Constitutional Council"},
		{"ar", "/law/333138/", "أعلن المجلس الدستوري عدم نفاذه"},
	} {
		r := httptest.NewRequest("GET", c.path, nil)
		r = r.WithContext(context.WithValue(r.Context(), langCtxKey, c.lang))
		out, err := s.execute(r, "law", Page{Title: "x", Path: "/law/333138/", Lang: "ar", Data: law})
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		for _, w := range []string{c.want, "5/2025", src} {
			if !strings.Contains(out, w) {
				t.Errorf("%s: no %q", c.path, w)
			}
		}
	}
	if got := lawAnswer(en, law); strings.Contains(got, "No repeal recorded") || !strings.Contains(got, "declared it not in force (decision 5/2025") {
		t.Errorf("answer line: %q", got)
	}
}
