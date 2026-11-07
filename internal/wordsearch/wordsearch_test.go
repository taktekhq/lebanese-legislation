package wordsearch

import (
	"math"
	"reflect"
	"testing"
)

func TestKeyRoundTrip(t *testing.T) {
	for _, k := range []Key{
		{},
		{Units: 3, Code: true, Titled: true, Score: 6.060792922973633e-05, Tie: 738773, ID: 2000200594, Position: 1107},
		{Units: 1, Score: 16.445171787514894, ID: 245560, Position: 9},
	} {
		got, ok := ParseKey(k.String())
		if !ok || *got != k {
			t.Errorf("%+v: read back %+v %v", k, got, ok)
		}
	}
	for _, bad := range []string{"", "0", "1~0~0~1~0~1", "1~2~0~1~0~1~0", "1~0~0~NaN~0~1~0", "1~0~0~Inf~0~1~0",
		"1~0~0~1~0~99999999999~0", "-1~0~0~1~0~1~0", "1~0~0~x~0~1~0"} {
		if _, ok := ParseKey(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	if k, _ := ParseKey(Key{Score: math.SmallestNonzeroFloat64}.String()); k.Score != math.SmallestNonzeroFloat64 {
		t.Error("a score does not read back to the same float")
	}
}

func TestForms(t *testing.T) {
	got := forms([]Term{{Text: "والخامسة"}, {Text: "وكالة"}, {Text: "عقد"}, {Text: "الجنسية", Alts: []string{"التابعية"}}, {Text: "contrat"}})
	want := []form{
		{"والخامسة", 0, 1, false}, {"الخامسه", 0, stemWeight, false}, // without its "و", after ar_norm
		{"وكالة", 1, 1, false}, {"كاله", 1, stemWeight, false}, // the word as written stays a form
		{"عقد", 2, 1, false}, // three letters: no "و" to set aside
		{"الجنسية", 3, 1, false}, {"التابعية", 3, stemWeight, true},
		{"contrat", 4, 1, false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("forms:\n got %v\nwant %v", got, want)
	}
	if f := forms([]Term{{Or: []string{"l.number_int = $1"}, Args: []any{5}}}); len(f) != 0 {
		t.Errorf("a term held by its condition alone has no form: %v", f)
	}
}

func TestRenumber(t *testing.T) {
	if got := renumber("l.kind = ANY($1) OR l.number_int = $2 OR x = $12", 4); got != "l.kind = ANY($5) OR l.number_int = $6 OR x = $16" {
		t.Errorf("renumber: %s", got)
	}
}

func TestWords(t *testing.T) {
	if got := Words(" «قانون» العمل،  (2015) "); !reflect.DeepEqual(got, []string{"قانون", "العمل", "2015"}) {
		t.Errorf("Words: %q", got)
	}
}

func TestAfterCond(t *testing.T) {
	var params []any
	got := afterCond([]keyCol{{"r.units", true, 2}, {"r.id", false, 7}}, &params)
	want := "(r.units < $2::int OR (r.units = $2::int AND (r.id > $1::int OR (r.id = $1::int AND false))))"
	if got != want || !reflect.DeepEqual(params, []any{7, 2}) {
		t.Errorf("afterCond: %s %v", got, params)
	}
}
