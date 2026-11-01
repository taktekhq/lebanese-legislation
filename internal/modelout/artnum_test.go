package modelout

import "testing"

func TestCanonEdges(t *testing.T) {
	numbers := map[int]map[string]bool{
		2: {"12": true, "12-1": true, "12-2": true, "19": true, "3": true, "8": true, "12 مكرر": true, "وحيدة": true},
		5: {"1": true, "2": true},
	}
	e := func(source, toArt, from string) edge {
		return edge{fromKey: "law/5/art/" + from, relation: "insert", source: source, status: "no_article", fromType: "legislation",
			fromID: 5, fromArticle: from, toLaw: 2, toArticle: toArt, toKey: "law/2", quote: "q"}
	}
	in := []edge{
		e("parser", "12-1", "1"), e("parser", "12-1", "1"), // the same twice
		e("model", "12/1", "1"), e("model", "12-1", "1"), // one article, two spellings: one edge
		e("model", "3,8", "2"),  // a list: one edge each
		e("model", "19.4", "2"), // no article 19-4: article 19, part 4
		e("model", "الوحيدة", "2"),
		e("model", "12 bis", "2"),
		e("model", "999", "2"),          // not in the text: kept as it is
		e("model", "البند الثاني", "2"), // no article at all
		{fromKey: "ruling/9", fromType: "ruling", fromID: 9, relation: "applies", source: "model", status: "unresolved", toArticle: "12/1", toCite: "x"},
	}
	var st ArtNumStats
	got := canonEdges(in, numbers, &st)
	type row struct{ source, from, art, part, status, to string }
	var rows []row
	for _, g := range got {
		rows = append(rows, row{g.source, g.fromArticle, g.toArticle, g.part, g.status, g.toKey})
	}
	want := []row{
		{"parser", "1", "12-1", "", "linked", "law/2/art/12-1"},
		{"model", "1", "12-1", "", "linked", "law/2/art/12-1"},
		{"model", "2", "3", "", "linked", "law/2/art/3"},
		{"model", "2", "8", "", "linked", "law/2/art/8"},
		{"model", "2", "19", "4", "linked", "law/2/art/19"},
		{"model", "2", "وحيدة", "", "linked", "law/2/art/وحيدة"},
		{"model", "2", "12 مكرر", "", "linked", "law/2/art/12 مكرر"},
		{"model", "2", "999", "", "no_article", "law/2"},
		{"model", "2", "", "البند الثاني", "no_article", "law/2"},
		{"model", "", "12-1", "", "unresolved", ""},
	}
	if len(rows) != len(want) {
		t.Fatalf("%d edges, want %d: %+v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("edge %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
	if st.Merged != 2 || st.Split != 1 || st.Parts != 1 || st.Unreadable != 1 || st.NotFound != 1 {
		t.Errorf("stats %+v", st)
	}
}
