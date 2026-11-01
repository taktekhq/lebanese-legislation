package modelout

import (
	"testing"
	"time"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/amend"
	"github.com/taktekhq/lebanese-legislation/internal/cite"
)

func TestRulingLinks(t *testing.T) {
	ch := func(action, art string, valid bool) cite.Change {
		return cite.Change{Action: action, Target: cite.Target{Kind: "law", Name: "x", Article: art}, Quote: "q", Valid: valid}
	}
	cs := map[string]cite.Citation{
		"ruling/5": {Key: "ruling/5", Model: "claude:sonnet"},
		"ruling/9": {Key: "ruling/9", Model: "claude:sonnet"}, // gone from the data
	}
	resolved := map[string][]cite.Link{
		"ruling/5": {
			{Change: ch("applies", "260", true), LawID: 2, How: "name"},    // the article is ours
			{Change: ch("cites", "999", true), LawID: 2, How: "name"},      // the text is, the article is not
			{Change: ch("interprets", "", true), LawID: 2, How: "name"},    // the text as a whole
			{Change: ch("applies", "1", true), LawID: 0, How: "not found"}, // not in our data
			{Change: ch("applies", "5", false), LawID: 2, How: "name"},     // quote not in the ruling: not used
		},
		"ruling/9": {{Change: ch("applies", "260", true), LawID: 2}},
	}
	dates := map[int]time.Time{5: time.Date(2005, 1, 1, 0, 0, 0, 0, time.UTC)}
	numbers := map[int]map[string]bool{2: {"260": true, "1": true}}
	got := rulingLinks(cs, resolved, dates, numbers)
	want := []struct{ rel, status, to string }{
		{"applies", "linked", "law/2/art/260"},
		{"cites", "no_article", "law/2"},
		{"interprets", "linked", "law/2"},
		{"applies", "unresolved", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("%d edges, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		e := got[i]
		if e.relation != w.rel || e.status != w.status || e.toKey != w.to {
			t.Errorf("edge %d = %s %s %q, want %v", i, e.relation, e.status, e.toKey, w)
		}
		if e.fromKey != "ruling/5" || e.fromType != "ruling" || e.fromID != 5 || e.source != "model" || e.model != "claude:sonnet" {
			t.Errorf("edge %d from %+v", i, e)
		}
	}
	if got[3].toCite != "lb/law/x/art/1" {
		t.Errorf("to_cite = %q", got[3].toCite)
	}
}

// The model linked Decree-law 112/1983 article 13
// to Penal Code article 51, as the Centre's text names it; the wording is
// article 251's, and db/amendments.tsv rejects the link. Only that link goes.
func TestDropRejectedModelLink(t *testing.T) {
	ds, err := amend.ParseDecisions(db.Amendments)
	if err != nil {
		t.Fatal(err)
	}
	link := func(source string, from int, fromArt string, to int, toArt string) edge {
		return edge{source: source, fromType: "legislation", fromID: from, fromArticle: fromArt, toLaw: to, toArticle: toArt,
			relation: "replace", status: "linked"}
	}
	edges := []edge{
		link("model", 244683, "13", 244611, "51"),  // rejected
		link("model", 244683, "14", 244611, "235"), // the next article's link stands
		link("model", 202271, "4", 244611, "51"),   // another text's link to 51 stands
		link("centre", 244683, "13", 244611, "51"), // a decision takes the model's links only here
		{source: "model", fromType: "ruling", fromID: 244683, fromArticle: "13", toLaw: 244611, toArticle: "51"},
	}
	got, n := dropRejected(edges, ds)
	if n != 1 || len(got) != 4 {
		t.Fatalf("dropped %d, kept %d: %+v", n, len(got), got)
	}
	for _, e := range got {
		if e.source == "model" && e.fromType == "legislation" && e.fromID == 244683 && e.fromArticle == "13" {
			t.Errorf("rejected link kept: %+v", e)
		}
	}
}
