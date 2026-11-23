package site

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An answer's sources are exact pages: one article, one ruling, one
// explanatory document or topic. Never a search, a list or a whole law
// (a law student, 23 Nov 2026: "every claim must point to the exact
// article or ruling page").
func TestAnswerSourcesAreExactPages(t *testing.T) {
	for path, want := range map[string]bool{
		"/law/190374/article/39/":             true,
		"/en/decree/2/article/1-%D8%A7/":      true,
		"/text/177149/article/36/":            true,
		"/ruling/86862/":                      true,
		"/topic/rent-law/":                    true,
		"/en/topic/end-of-service-indemnity/": true,
		"/research/19/":                       true,
		"/search/?q=%D8%B9%D9%85%D9%84":       false,
		"/en/search/?q=leave":                 false,
		"/search/":                            false,
		"/law/190374/":                        false,
		"/laws/":                              false,
		"/rulings/":                           false,
		"/law/1/article/5/?q=x":               false,
		"/api/v1/search/?q=x":                 false,
		"https://example.com/ruling/1/":       false,
		"":                                    false,
	} {
		if got := exactSourcePath(path); got != want {
			t.Errorf("exactSourcePath(%q) = %v, want %v", path, got, want)
		}
	}
	in := []answerSource{{Path: "/search/?q=x"}, {Path: "/law/1/article/2/"}, {Path: "/law/1/"}, {Path: "/ruling/3/"}}
	out := exactSources(in)
	if len(out) != 2 || out[0].Path != "/law/1/article/2/" || out[1].Path != "/ruling/3/" {
		t.Errorf("exactSources kept %+v", out)
	}
}

// Every paragraph says what it rests on: the text of a law and which
// article, a court ruling in one case, or neither.
func TestAnswerBasisLabels(t *testing.T) {
	s, err := newServer(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	view := func() aiView {
		return aiView{
			Sources: []aiSource{{answerSource: answerSource{Path: "/law/190374/article/39/"}}, {answerSource: answerSource{Path: "/ruling/86862/"}}, {answerSource: answerSource{Path: "/en/topic/rent-law/"}}},
			Paras:   []aiPara{{Text: "a", Refs: []int{1}}, {Text: "b", Refs: []int{2}}, {Text: "c", Refs: []int{3}}, {Text: "d", Refs: []int{1, 2}}},
		}
	}
	en := view()
	req := func(lang string) *http.Request {
		r := httptest.NewRequest("POST", "/search/ai/", nil)
		return r.WithContext(context.WithValue(r.Context(), langCtxKey, lang))
	}
	s.labelBases(req("en"), &en)
	for i, want := range []string{"Basis: the text of the law, Article 39", "Basis: a court ruling in one case", "Basis: no law text or court ruling", "Article 39"} {
		if !strings.Contains(en.Paras[i].Basis, want) {
			t.Errorf("paragraph %d basis %q, want %q", i, en.Paras[i].Basis, want)
		}
	}
	if !strings.Contains(en.Paras[3].Basis, "court ruling") {
		t.Errorf("a paragraph on a law and a ruling names both: %q", en.Paras[3].Basis)
	}
	ar := view()
	s.labelBases(req("ar"), &ar)
	if !strings.Contains(ar.Paras[0].Basis, "المادة 39") || !strings.Contains(ar.Paras[1].Basis, "حكم قضائي في قضية واحدة") || !strings.Contains(ar.Paras[2].Basis, "لا نص قانوني") {
		t.Errorf("Arabic bases: %q / %q / %q", ar.Paras[0].Basis, ar.Paras[1].Basis, ar.Paras[2].Basis)
	}

	// The answer shows each basis under its paragraph.
	var b bytes.Buffer
	en.Paras = en.Paras[:1]
	if err := s.pages["search"].ExecuteTemplate(&b, "ai_answer", en); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `class="ai-box__basis"`) || !strings.Contains(b.String(), "Basis: the text of the law, Article 39") {
		t.Errorf("no basis line: %.600s", b.String())
	}
}
