package site

import (
	"html/template"
	"strings"
	"testing"
	"time"
)

func TestArticleHistoryRendersNewestFirst(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	oldText, newText := "old-wording", "current-wording"
	a := &Article{Number: "11", Versions: []Version{
		{Major: 1, From: &old, State: "in_force", TextAr: &oldText, Source: "centre"},
		{Major: 2, From: &recent, State: "in_force", TextAr: &newText, Source: "amendment"},
	}}
	law := &Law{LawRow: LawRow{ID: 244565, Kind: "مرسوم", Title: "x", Status: "in_force"}}
	for lang, pages := range map[string]map[string]*template.Template{"en": s.pages, "ar": s.pagesAr} {
		t.Run(lang, func(t *testing.T) {
			var b strings.Builder
			if err := pages["law"].ExecuteTemplate(&b, "article", map[string]any{"Law": law, "A": a, "Standalone": true}); err != nil {
				t.Fatal(err)
			}
			_, history, ok := strings.Cut(b.String(), `<ol class="hist">`)
			if !ok {
				t.Fatal("history missing")
			}
			latest, earliest := strings.Index(history, "current-wording"), strings.Index(history, "old-wording")
			if latest < 0 || earliest < 0 || latest > earliest {
				t.Error("history must show newest wording first")
			}
		})
	}
	if a.Latest().Major != 2 || a.Current().Major != 2 || a.Shown() != newText || a.Versions[0].Major != 1 {
		t.Fatal("rendering changed current wording or internal version order")
	}
}

func TestCouncilListsNewestFirst(t *testing.T) {
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	law := &Law{List: []*Article{{Number: "11"}}}
	var rows []CouncilRuling
	for _, d := range []struct {
		label string
		on    *time.Time
	}{{"old", &old}, {"undated", nil}, {"new", &recent}} {
		rows = append(rows, CouncilRuling{Decision: d.label, On: d.on}, CouncilRuling{Decision: d.label, On: d.on, Number: "11"})
	}
	law.attachCouncil(rows)
	for name, got := range map[string][]CouncilRuling{"article": law.List[0].Council} {
		t.Run(name, func(t *testing.T) {
			for i, want := range []string{"new", "old", "undated"} {
				if got[i].Decision != want {
					t.Errorf("item %d: got %s want %s", i, got[i].Decision, want)
				}
			}
		})
	}
	for i, want := range []string{"new", "old", "undated"} {
		if law.Council[i].Decision != want {
			t.Errorf("law item %d: got %s want %s", i, law.Council[i].Decision, want)
		}
		if len(law.Council[i].Articles) != 1 {
			t.Error("decision article grouping lost")
		}
	}
}
