package site

import (
	"golang.org/x/net/html"
	"strings"
	"testing"
)

// Amendment wording and every version remain available, but must be inside
// a closed disclosure while the article's current wording remains readable.
func TestArticleAmendmentsCollapsed(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	law := &Law{LawRow: LawRow{ID: 244565, Kind: "مرسوم"}}
	change := &Change{Action: "replace", By: LawRow{ID: 440, Kind: "قانون"}, ByArticle: "1", TextAr: ptr("نص التعديل")}
	cases := map[string]*Article{
		"links":     {Number: "77", TextAr: "النص الحالي", Links: []Link{{Relation: "replace", By: change.By, ByArticle: "1", Text: "نص التعديل"}}},
		"versions":  {Number: "77", Versions: []Version{{Major: 1, State: "in_force", TextAr: ptr("النص السابق"), Source: "centre"}, {Major: 2, State: "in_force", TextAr: ptr("النص الحالي"), Change: change, Source: "amendment"}}},
		"pending":   {Number: "77", TextAr: "النص الحالي", Versions: []Version{{Major: 1, State: "in_force", TextAr: ptr("النص الحالي"), Source: "centre"}, {Major: 2, State: "in_force", Change: change}}},
		"unsure":    {Number: "77", TextAr: "النص الحالي", Unsure: []Change{*change}},
		"repealed":  {Number: "77", Versions: []Version{{Major: 1, State: "in_force", TextAr: ptr("النص السابق")}, {Major: 2, State: "repealed", Change: change}}},
		"unchanged": {Number: "77", TextAr: "النص الحالي"},
	}
	for name, a := range cases {
		for _, lang := range []string{"en", "ar"} {
			for _, standalone := range []bool{false, true} {
				t.Run(name+"/"+lang+"/"+map[bool]string{true: "single", false: "law"}[standalone], func(t *testing.T) {
					pages := s.pages
					if lang == "ar" {
						pages = s.pagesAr
					}
					var b strings.Builder
					if err := pages["law"].ExecuteTemplate(&b, "article", map[string]any{"Law": law, "A": a, "Standalone": standalone}); err != nil {
						t.Fatal(err)
					}
					doc, err := html.Parse(strings.NewReader(b.String()))
					if err != nil {
						t.Fatal(err)
					}
					panels := 0
					visible := []string{}
					hidden := []string{}
					var walk func(*html.Node, bool)
					walk = func(n *html.Node, collapsed bool) {
						cls := ""
						open := false
						for _, attr := range n.Attr {
							if attr.Key == "class" {
								cls = attr.Val
							}
							if attr.Key == "open" {
								open = true
							}
						}
						if n.Data == "details" && strings.Contains(cls, "art__amendments") {
							panels++
							if open {
								t.Error("amendments open by default")
							}
						}
						if n.Type == html.TextNode {
							if collapsed {
								hidden = append(hidden, n.Data)
							} else {
								visible = append(visible, n.Data)
							}
						}
						for c := n.FirstChild; c != nil; c = c.NextSibling {
							walk(c, collapsed || (n.Data == "details" && !open && c.Data != "summary"))
						}
					}
					walk(doc, false)
					if name == "unchanged" {
						if panels != 0 {
							t.Error("empty amendment banner")
						}
						return
					}
					if panels != 1 {
						t.Fatalf("got %d amendment disclosures, want one", panels)
					}
					shown := strings.Join(visible, " ")
					folded := strings.Join(hidden, " ")
					if strings.Contains(shown, "نص التعديل") || strings.Contains(shown, "النص السابق") {
						t.Error("history leaked into current wording")
					}
					if !a.Repealed() && !strings.Contains(shown, "النص الحالي") {
						t.Error("current wording hidden")
					}
					if name != "versions" && name != "repealed" && !strings.Contains(folded, "نص التعديل") {
						t.Error("amending wording missing from disclosure")
					}
					if (name == "versions" || name == "repealed") && !strings.Contains(folded, "النص السابق") {
						t.Error("version history lost")
					}
				})
			}
		}
	}
}
