package amend

import (
	"strconv"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/db"
)

// The decisions file the build embeds reads whole, and carries the Penal
// Code review's rows (Oct 2026).
func TestDecisionsFile(t *testing.T) {
	ds, err := ParseDecisions(db.Amendments)
	if err != nil {
		t.Fatal(err)
	}
	want := map[[2]string]string{
		{"2000200346/4", "244611/43"}:  DecideRepeal, // Law 68/2026 repeals Penal Code 43
		{"2000200346/4", "244483/420"}: DecideRepeal, // and Code of Criminal Procedure 420 to 424
		{"2000200346/4", "244483/424"}: DecideRepeal,
		{"244683/13", "244611/51"}:     DecideReject, // the Centre's "51" is article 251
	}
	for _, d := range ds {
		k := [2]string{strconv.Itoa(d.ByLaw) + "/" + d.ByArticle, strconv.Itoa(d.Law) + "/" + d.Article}
		if w, ok := want[k]; ok {
			if d.Decision != w {
				t.Errorf("%v: decision %q, want %q", k, d.Decision, w)
			}
			if d.Note == "" {
				t.Errorf("%v: no note", k)
			}
			delete(want, k)
		}
	}
	for k := range want {
		t.Errorf("%v: no row", k)
	}
}

func TestParseDecisionsRefusesUnknown(t *testing.T) {
	for _, s := range []string{
		"1\t2\t3\t4\tdelete\tx",    // a decision the build doesn't know
		"1\t2\t3\t4",               // too few columns
		"x\t2\t3\t4\treject\tnote", // not an id
	} {
		if _, err := ParseDecisions("by_law_id\tby_article\tlaw_id\tarticle\tdecision\tnote\n" + s); err == nil {
			t.Errorf("%q: no error", s)
		}
	}
	ds, err := ParseDecisions("# a comment\r\n\n1\t2\t3\t4\trepeal\r\n")
	if err != nil || len(ds) != 1 || !ds[0].Matches(1, "2", 3, "4") || strings.Contains(ds[0].Line, "\r") {
		t.Errorf("got %+v, %v", ds, err)
	}
}
