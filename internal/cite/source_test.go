package cite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/amend"
)

const rulingText = "وحيث ان المحكمة تطبق عملا باحكام المادة 260 من قانون الموجبات والعقود وكذلك المادة 73 من القانون رقم 328 تاريخ 2 آب 2001"

func TestSourceByName(t *testing.T) {
	for name, want := range map[string]string{"": "articles", "articles": "articles", "rulings": "rulings"} {
		s, err := SourceByName(name)
		if err != nil || s.Name != want {
			t.Errorf("SourceByName(%q) = %q, %v", name, s.Name, err)
		}
	}
	if _, err := SourceByName("opinions"); err == nil {
		t.Error("an unknown source must be an error")
	}
	if Rulings.Prompt == Articles.Prompt {
		t.Error("rulings and articles need their own prompt versions")
	}
}

func TestRulingKey(t *testing.T) {
	if k := RulingKey(50738); k != "ruling/50738" || RulingID(k) != 50738 {
		t.Errorf("key %q, id %d", k, RulingID(k))
	}
	if RulingID("12/3") != 0 {
		t.Error("an article key is not a ruling")
	}
}

func TestParseBatchAroundProse(t *testing.T) {
	got, err := parseBatch("Here you go:\n```json\n{\"items\":[{\"key\":\"ruling/1\",\"amends\":false,\"changes\":[{\"action\":\"applies\",\"kind\":\"law\",\"quote\":\"q\"}]}]}\n```")
	if err != nil || len(got["ruling/1"].Changes) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := parseBatch("no json here"); err == nil {
		t.Error("an answer without JSON must be an error")
	}
	if _, err := parseBatch("  "); err == nil {
		t.Error("an empty answer must be an error")
	}
}

func TestBatches(t *testing.T) {
	var items []Item
	for i := 0; i < 5; i++ {
		items = append(items, Item{Key: RulingKey(i), Text: strings.Repeat("ا", 100)})
	}
	part := func(it Item) string { return it.Text }
	if got := batches(items, 2, 0, part); len(got) != 3 || len(got[2]) != 1 {
		t.Errorf("by count: %d batches", len(got))
	}
	// 100 letters are 200 bytes: three never fit in 500.
	if got := batches(items, 10, 500, part); len(got) != 3 || len(got[0]) != 2 {
		t.Errorf("by size: %d batches, first %d", len(got), len(got[0]))
	}
	// An item over the cap goes alone, never dropped.
	big := append([]Item{{Key: "a", Text: strings.Repeat("ا", 1000)}}, items[0])
	if got := batches(big, 10, 500, part); len(got) != 2 || len(got[0]) != 1 {
		t.Errorf("big item: %d batches", len(got))
	}
}

func TestRulingPrompt(t *testing.T) {
	p := Rulings.prompt([]Item{{Key: "ruling/7", Identity: "تمييز مدني رقم 14/1999", Title: "عنوان", Text: rulingText}})
	for _, want := range []string{"### key: ruling/7", rulingText, "applies, cites, interprets", "The rulings:"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

func TestRulingQuoteCheck(t *testing.T) {
	a := answer{Changes: []struct {
		Action, Kind, Number, Date, Name, Article, Part, Quote, Why string
		Year                                                        int
		Self                                                        bool
	}{
		{Action: "applies", Kind: "law", Name: "قانون الموجبات والعقود", Article: "260", Quote: "عملا باحكام المادة 260 من قانون الموجبات والعقود"},
		{Action: "applies", Kind: "law", Number: "9", Article: "1", Quote: "المادة 1 من القانون رقم 9"}, // not in the ruling
	}}
	got := check(rulingText, a)
	if !got[0].Valid || got[1].Valid {
		t.Errorf("valid = %v, %v; want true, false", got[0].Valid, got[1].Valid)
	}
}

func rulingIndex() *amend.Index {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	return amend.NewIndex([]amend.Law{
		{ID: 1, Kind: "قانون", Number: "328", Issued: d(2001, 8, 2), Title: "قانون تعديل بعض احكام قانون اصول المحاكمات الجزائية"},
		{ID: 2, Kind: "قانون", Number: "", Issued: d(1932, 3, 9), Title: "قانون الموجبات والعقود", InForce: true, Code: true},
		{ID: 3, Kind: "قانون", Number: "75", Issued: d(1999, 1, 1), Title: "قانون أ"},
		{ID: 4, Kind: "قانون", Number: "75", Issued: d(2010, 1, 1), Title: "قانون ب"},
	})
}

func TestResolveRulings(t *testing.T) {
	ix := rulingIndex()
	ch := func(tg Target) Change { return Change{Action: "applies", Target: tg, Quote: "q", Valid: true} }
	cs := map[string]Citation{
		"ruling/1": {Key: "ruling/1", Changes: []Change{
			ch(Target{Kind: "law", Number: "328", Date: "2001-08-02", Article: "73"}),
			ch(Target{Kind: "law", Name: "قانون الموجبات والعقود", Article: "260"}),
			ch(Target{Kind: "law", Self: true}), // a ruling has no "this law"
			ch(Target{Kind: "law", Number: "75", Year: 2010}),
		}},
		// The same citation, from a ruling decided later.
		"ruling/2": {Key: "ruling/2", Changes: []Change{ch(Target{Kind: "law", Number: "75", Year: 2010})}},
	}
	got := ResolveRulings(ix, cs, map[int]time.Time{1: time.Date(2005, 1, 1, 0, 0, 0, 0, time.UTC), 2: time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC)})
	if got["ruling/1"][0].LawID != 1 {
		t.Errorf("number and date: %+v", got["ruling/1"][0])
	}
	if got["ruling/1"][1].LawID != 2 {
		t.Errorf("name: %+v", got["ruling/1"][1])
	}
	if got["ruling/1"][2].LawID != 0 {
		t.Errorf("self in a ruling: %+v", got["ruling/1"][2])
	}
	// Decided in 2005, the ruling can only cite the 1999 text 75.
	if got["ruling/1"][3].LawID != 0 {
		t.Errorf("a text after the ruling's date is not cited: %+v", got["ruling/1"][3])
	}
	if got["ruling/2"][0].LawID != 4 {
		t.Errorf("ruling 2: %+v", got["ruling/2"][0])
	}
}

// A fake claude: prints a fixed answer for ruling/1 whatever it is asked.
func fakeClaude(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\ncat <<'EOF'\n" + body + "\nEOF\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunBatchesWithClaude(t *testing.T) {
	bin := fakeClaude(t, `Sure.
{"items":[{"key":"ruling/1","amends":false,"changes":[
 {"action":"applies","kind":"law","name":"قانون الموجبات والعقود","article":"260","quote":"عملا باحكام المادة 260 من قانون الموجبات والعقود"},
 {"action":"cites","kind":"law","number":"9","article":"1","quote":"words that are not in the ruling"}]}]}`)
	be, err := NewClaude(context.Background(), bin, "", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer be.Cleanup()
	if be.Name() != "claude:sonnet" {
		t.Errorf("model = %q, want the default claude:sonnet", be.Name())
	}
	path := filepath.Join(t.TempDir(), "ruling_citations.jsonl")
	items := []Item{{Key: "ruling/1", Input: "h1", Text: rulingText, Identity: "x", Title: "y"}}
	var logs []string
	st, err := RunBatches(context.Background(), be, Rulings, items, path, 2, 8, 0, "", func(f string, a ...any) { logs = append(logs, f) })
	if err != nil || st.Asked != 1 || st.Failed != 0 || st.Changes != 2 || st.Invalid != 1 || st.Calls != 1 {
		t.Fatalf("stats %+v, %v", st, err)
	}
	have, _ := Load(path)
	c := have["ruling/1"]
	if c.Model != "claude:sonnet" || c.Prompt != RulingPrompt || c.Input != "h1" || !c.Changes[0].Valid || c.Changes[1].Valid {
		t.Errorf("saved %+v", c)
	}
	// Read again with the same input: skipped. Another input: asked again.
	st, _ = RunBatches(context.Background(), be, Rulings, items, path, 1, 8, 0, "", func(string, ...any) {})
	if st.Asked != 0 || st.Skipped != 1 {
		t.Errorf("second run %+v", st)
	}
	items[0].Input = "h2"
	st, _ = RunBatches(context.Background(), be, Rulings, items, path, 1, 8, 0, "", func(string, ...any) {})
	if st.Asked != 1 {
		t.Errorf("changed input %+v", st)
	}
}

func TestRunBatchesClaudeFails(t *testing.T) {
	be, err := NewClaude(context.Background(), fakeClaude(t, "I cannot help with that."), "", "opus", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer be.Cleanup()
	path := filepath.Join(t.TempDir(), "c.jsonl")
	st, err := RunBatches(context.Background(), be, Rulings, []Item{{Key: "ruling/1", Input: "h", Text: rulingText}}, path, 1, 8, 0, "", func(string, ...any) {})
	if err != nil || st.Asked != 0 || st.Failed != 1 {
		t.Errorf("stats %+v, %v", st, err)
	}
	if be.Name() != "claude:opus" {
		t.Errorf("model = %q", be.Name())
	}
}

// The Centre's notes in an article name the texts that changed it later.
func TestResolveNoteLater(t *testing.T) {
	ix := rulingIndex() // text 3: law 75 of 1999, text 4: law 75 of 2010, text 1: law 328 of 2001
	later := Target{Kind: "law", Number: "75", Year: 2010}
	cs := map[string]Citation{
		"3/1": {Key: "3/1", Changes: []Change{{Action: "note_amended_by", Target: later, Quote: "q", Valid: true}}},
		"1/1": {Key: "1/1", Changes: []Change{{Action: "cites", Target: later, Quote: "q", Valid: true}}},
	}
	got := ResolveAll(ix, cs)
	if got["3/1"][0].LawID != 4 {
		t.Errorf("a note naming a later text: %+v", got["3/1"][0])
	}
	if got["1/1"][0].LawID != 0 {
		t.Errorf("a citation of a later text should not resolve: %+v", got["1/1"][0])
	}
}
