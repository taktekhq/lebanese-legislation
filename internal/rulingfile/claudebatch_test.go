package rulingfile

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestCustomIDRoundTrip(t *testing.T) {
	for _, k := range []string{"5388:1,2,3", "87791:12", "100:1,10,11"} {
		id := customID(k)
		for _, r := range id {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				t.Fatalf("%q: bad character %q in %q", k, r, id)
			}
		}
		if back := keyOf(id); back != k {
			t.Errorf("%q -> %q -> %q", k, id, back)
		}
	}
}

func message(t *testing.T, stop, text string) anthropic.Message {
	var m anthropic.Message
	b, _ := json.Marshal(map[string]any{"id": "m", "type": "message", "role": "assistant", "model": Haiku,
		"stop_reason": stop, "content": []any{map[string]any{"type": "text", "text": text}},
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1}})
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestClaudePages(t *testing.T) {
	got, _, err := claudePages(message(t, "end_turn", "=== صفحة 2 ===\nنص الصفحة الثانية\n=== صفحة 3 ===\nنص الصفحة الثالثة"), []int{2, 3})
	if err != nil || got[2] == "" || got[3] == "" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, _, err := claudePages(message(t, "max_tokens", "=== صفحة 2 ===\nنص"), []int{2}); err == nil {
		t.Error("a cut-off answer must give nothing")
	}
	if _, _, err := claudePages(message(t, "refusal", ""), []int{2}); err == nil {
		t.Error("a refusal must give nothing")
	}
	got, _, _ = claudePages(message(t, "end_turn", "=== صفحة 4 ===\nنص"), []int{4, 5})
	if _, ok := got[5]; ok || got[4] == "" {
		t.Errorf("a page left out of the answer must stay pending: %v", got)
	}
}

func TestClaudePagesDeclined(t *testing.T) {
	no := "I can't produce a verbatim transcription of these pages. The scans are handwritten and partly illegible."
	for _, want := range [][]int{{1, 2, 3}, {4}} {
		got, declined, err := claudePages(message(t, "end_turn", no), want)
		if err != nil || !declined || len(got) != 0 {
			t.Errorf("%v: got %v, declined %v, %v", want, got, declined, err)
		}
	}
	if _, declined, _ := claudePages(message(t, "end_turn", "=== صفحة 1 ===\nنص"), []int{1}); declined {
		t.Error("a transcription is not a refusal")
	}
}

func TestPagePlanHaikuDeclined(t *testing.T) {
	r := DefaultRouter
	typed := TriagePage{N: 1, Conf: 80, Words: 200}
	hand := TriagePage{N: 1, Conf: 30, Words: 200}
	d := map[string]string{HaikuDeclined: "1"}
	if pl := r.PagePlan(1, typed, d, nil); pl.A != Gemini || pl.B != Sonnet || slices.Contains(pl.Need, Haiku) {
		t.Errorf("typed, Haiku declined: %+v", pl)
	}
	if pl := r.PagePlan(1, typed, map[string]string{HaikuDeclined: "1", GeminiBlocked: "2"}, nil); pl.A != Sonnet || pl.B != Opus || slices.Contains(pl.Need, Haiku) {
		t.Errorf("typed, both declined: %+v", pl)
	}
	if pl := r.PagePlan(1, hand, map[string]string{HaikuDeclined: "1", GeminiBlocked: "2"}, nil); slices.Contains(pl.Need, Haiku) {
		t.Errorf("handwritten, both declined: %+v", pl)
	}
}

func TestSaveKeepsOtherModelsBatches(t *testing.T) {
	dir := t.TempDir()
	haiku := &ClaudeBatch{Dir: dir, Model: Haiku}
	sonnet := &ClaudeBatch{Dir: dir, Model: Sonnet}
	os.MkdirAll(haiku.stateDir(), 0o755)
	// Both units load the file, then each saves what it holds.
	hs, _ := haiku.load()
	ss, _ := sonnet.load()
	if err := haiku.save(append(hs, &Batch{Name: "h1", Model: Haiku})); err != nil {
		t.Fatal(err)
	}
	if err := sonnet.save(append(ss, &Batch{Name: "s1", Model: Sonnet})); err != nil {
		t.Fatal(err)
	}
	all, _ := haiku.load()
	names := map[string]bool{}
	for _, b := range all {
		names[b.Name] = true
	}
	if len(all) != 2 || !names["h1"] || !names["s1"] {
		t.Fatalf("a save must not drop the other model's batches: %+v", all)
	}
}

func TestClaudeReaderOwedOnlyUnread(t *testing.T) {
	tr := &Triage{ID: 3, Pages: []TriagePage{{N: 1, Conf: 85, Words: 100}, {N: 2, Conf: 85, Words: 100}, {N: 3, Conf: 30, Words: 100}}}
	// Gemini read page 1 and refused pages 2 and 3 twice: Claude owes them.
	rs := Readings{1: {Gemini: "نص"}, 2: {GeminiBlocked: "2"}, 3: {GeminiBlocked: "2"}}
	c := &ClaudeReader{Router: DefaultRouter, OnlyUnread: true}
	if got := c.owed(tr, nil, rs, Haiku); !slices.Equal(got, []int{2}) {
		t.Errorf("Haiku owes %v, want [2]: page 1 is read, page 3 is handwriting", got)
	}
	if got := c.owed(tr, nil, rs, Opus); !slices.Equal(got, []int{3}) {
		t.Errorf("Opus owes %v, want [3]", got)
	}
	if got := c.owed(tr, nil, rs, Sonnet); slices.Contains(got, 3) {
		t.Errorf("an unread handwritten page is Opus's alone, Sonnet owes %v", got)
	}
	c.OnlyUnread = false
	if got := c.owed(tr, nil, rs, Haiku); !slices.Contains(got, 1) {
		t.Errorf("without OnlyUnread Haiku still owes its second reading of page 1: %v", got)
	}
}
