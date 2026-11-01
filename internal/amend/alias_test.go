package amend

import (
	"strings"
	"testing"
	"time"

	"github.com/taktekhq/lebanese-legislation/db"
)

func TestParseNameAliases(t *testing.T) {
	m, err := ParseNameAliases("name\tlaw_id\tnote\n# a comment\n\nقانون الجمعيات\t257670\tthe 1909 law\nالجمعيات\t257670\tsame name, folded alike\n")
	if err != nil || len(m) != 1 || m[titleKey("قانون الجمعيات")] != 257670 {
		t.Fatalf("got %v, %v", m, err)
	}
	for _, bad := range []string{
		"قانون الجمعيات\tabc\tnote",                    // not an id
		"قانون الجمعيات\t12",                           // no note
		"قانون الجمعيات\t12\tn\nقانون الجمعيات\t13\tn", // the same name, two texts
	} {
		if _, err := ParseNameAliases(bad); err == nil {
			t.Errorf("%q should be an error", bad)
		}
	}
}

// The shipped file parses, and has no duplicate or malformed row.
func TestShippedAliases(t *testing.T) {
	m, err := ParseNameAliases(db.Aliases)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) < 5 {
		t.Errorf("only %d aliases", len(m))
	}
	if strings.ContainsRune(db.Aliases, rune(0x2014)) {
		t.Error("an em dash in aliases.tsv")
	}
}

func TestResolveLaterExact(t *testing.T) {
	d := func(y int) time.Time { return time.Date(y, 3, 1, 0, 0, 0, 0, time.UTC) }
	ix := NewIndex([]Law{{ID: 7, Kind: "قانون منفذ بمرسوم", Number: "3398", Issued: d(1965), Title: "نص"}})
	r := Ref{Kind: KindDecree, Num: 3398, Number: "3398", Date: d(1965), Year: 1965}
	if id, _ := ix.Resolve(r, d(1963)); id != 0 {
		t.Errorf("off by default: a text after the citing one resolved to %d", id)
	}
	ix.AllowLater()
	id, how := ix.Resolve(r, d(1963))
	if id != 7 || !strings.Contains(how, "later") {
		t.Errorf("exact number and date: %d %q", id, how)
	}
	// Not for a number with only a year, nor a different date.
	if id, _ := ix.Resolve(Ref{Kind: KindDecree, Num: 3398, Number: "3398", Year: 1965}, d(1963)); id != 0 {
		t.Errorf("year only resolved to %d", id)
	}
	if id, _ := ix.Resolve(Ref{Kind: KindDecree, Num: 3398, Number: "3398", Date: d(1964), Year: 1964}, d(1963)); id != 0 {
		t.Errorf("another date resolved to %d", id)
	}
}

func TestResolveAlias(t *testing.T) {
	d := func(y int) time.Time { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }
	ix := NewIndex([]Law{
		{ID: 1, Kind: "قانون", Issued: d(1909), Title: "قانون الجمعيات صادر في 29 رجب سنة 1327 و3 اغسطس 1909"},
		{ID: 2, Kind: "قانون", Number: "244", Issued: d(2021), Title: "الشراء العام في لبنان"},
	})
	if missing := ix.SetNameAliases(map[string]int{titleKey("قانون الجمعيات"): 1, titleKey("قانون الشراء العام"): 2, titleKey("قانون غائب"): 99}); len(missing) != 1 || missing[0] != 99 {
		t.Errorf("missing = %v, want [99]", missing)
	}
	r := Ref{Name: FoldString("قانون الجمعيات")}
	if id, how := ix.Resolve(r, d(1950)); id != 1 || how != "alias" {
		t.Errorf("name: %d %q", id, how)
	}
	if id, how := ix.Resolve(r, time.Time{}); id != 1 {
		t.Errorf("no date: %d %q", id, how)
	}
	// The 2021 law cannot be what a 1990 text calls "قانون الشراء العام".
	r = Ref{Name: FoldString("قانون الشراء العام")}
	if id, how := ix.Resolve(r, d(1990)); id != 0 {
		t.Errorf("a text after the citing one: %d %q", id, how)
	}
	if id, _ := ix.Resolve(r, d(2023)); id != 2 {
		t.Errorf("after 2021: %d", id)
	}
	// A name that is not listed is not guessed.
	if id, _ := ix.Resolve(Ref{Name: FoldString("قانون الاثار")}, d(2000)); id != 0 {
		t.Errorf("unlisted name resolved to %d", id)
	}
}
