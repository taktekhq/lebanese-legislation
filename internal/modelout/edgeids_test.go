package modelout

import (
	"reflect"
	"testing"
)

func TestEdgeIDsFirstBuildCounts(t *testing.T) {
	edges := []edge{{fromKey: "law/1", toKey: "law/2", relation: "cites"}, {fromKey: "law/1", toKey: "law/3", relation: "cites"}}
	if got := edgeIDs(edges, nil); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("first build ids = %v, want [1 2]", got)
	}
}

// One edge gone from the middle: every other edge keeps its id, and the new
// edge takes the next id after the highest ever given, not the freed one.
func TestEdgeIDsKeepPriorIDs(t *testing.T) {
	a := edge{fromKey: "law/1", toKey: "law/2", relation: "cites", source: "model", status: "linked"}
	gone := edge{fromKey: "law/1", toKey: "law/3", relation: "cites", source: "model", status: "linked"}
	c := edge{fromKey: "gazette/9", toKey: "law/2", relation: "publishes", source: "centre", status: "linked", fromID: 9, toLaw: 2}
	fresh := edge{fromKey: "law/4", toCite: "lb/law/2001-08-02/328", relation: "cites", source: "model", status: "unresolved"}
	prior := map[string][]int{edgeIdentity(a): {1}, edgeIdentity(gone): {2}, edgeIdentity(c): {3}}
	got := edgeIDs([]edge{a, c, fresh}, prior)
	if want := []int{1, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

// The quote, how and model may change under the same id; what it links may not.
func TestEdgeIdentityIgnoresWording(t *testing.T) {
	a := edge{fromKey: "law/1/art/2", toKey: "law/5/art/3", relation: "replace", source: "parser", status: "linked", quote: "old", how: "number", model: "m1"}
	b := a
	b.quote, b.how, b.model = "new", "number and date", "m2"
	if edgeIdentity(a) != edgeIdentity(b) {
		t.Fatal("a changed quote, how or model made a new edge")
	}
	b.part = "الفقرة الثانية"
	if edgeIdentity(a) == edgeIdentity(b) {
		t.Fatal("a different part kept the same identity")
	}
}

// Identical edges (the same citation read twice) keep their ids in order.
func TestEdgeIDsDuplicates(t *testing.T) {
	d := edge{fromKey: "ruling/7", toKey: "law/2", relation: "cites", source: "model", status: "linked"}
	prior := map[string][]int{edgeIdentity(d): {5, 9}}
	if got := edgeIDs([]edge{d, d, d}, prior); !reflect.DeepEqual(got, []int{5, 9, 10}) {
		t.Fatalf("ids = %v, want [5 9 10]", got)
	}
}
