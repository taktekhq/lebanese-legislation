package build

import (
	"testing"
	"time"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/amend"
)

// Law 68/2026, article 4, as the data carries it (SaderLex page 200346).
const law68art4 = "تلغى المادة ٤٣ من قانون العقوبات والمواد من ٤٢٠ ضمنا حتى ٤٢٤ ضمنا من القانون رقم ٣٢٨ تاريخ ٢٠٠١/٨/٢ (قانون أصول المحاكمات الجزائية)."

// The pattern pass reads Law 68/2026 article 4 as
// a repeal of article 43 of a text it can't name, and misses the Code of
// Criminal Procedure half. The rows in db/amendments.tsv make Penal Code 43
// and CCP 420 to 424 repealed from the Gazette date, 20 Aug 2026; the
// reject on Decree-law 112/1983 article 13 adds nothing here.
func TestDecisionsRepealPenalCode43(t *testing.T) {
	ops := amend.Parse(law68art4)
	if len(ops) != 1 || ops[0].Action != amend.Repeal || len(ops[0].Articles) != 1 || ops[0].Articles[0] != "43" {
		t.Fatalf("the pattern pass now reads %+v: check the rows in db/amendments.tsv still need to be there", ops)
	}
	gazette := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	ix := amend.NewIndex([]amend.Law{{ID: 2000200346, Kind: "قانون", Number: "68",
		Issued: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), Gazette: gazette, InForce: true}})
	read := []*change{{byLaw: 2000200346, byArticle: "4", seq: 1, action: amend.Repeal, article: "43",
		effective: gazette, status: "review", reasons: []string{"name not found"}, method: "pattern"}}
	all, err := amend.ParseDecisions(db.Amendments)
	if err != nil {
		t.Fatal(err)
	}
	var ds []amend.Decision // this task's rows
	for _, d := range all {
		if d.ByLaw == 2000200346 || d.ByLaw == 244683 {
			ds = append(ds, d)
		}
	}
	amending := func(byLaw int, byArt string) (string, bool) {
		return law68art4, byLaw == 2000200346 && byArt == "4"
	}
	var notes []string
	note := func(f string, a ...any) { notes = append(notes, f) }
	got := applyDecisions(read, ds, ix, amending, note)

	byTarget := map[[2]any]*change{}
	for _, c := range got {
		if c.byLaw == 2000200346 {
			byTarget[[2]any{c.law, c.article}] = c
		}
		if c.byLaw == 244683 {
			t.Errorf("a reject added a change: %+v", c)
		}
	}
	for _, k := range [][2]any{{244611, "43"}, {244483, "420"}, {244483, "421"}, {244483, "422"}, {244483, "423"}, {244483, "424"}} {
		c := byTarget[k]
		if c == nil {
			t.Errorf("%v: not repealed", k)
			continue
		}
		if c.action != amend.Repeal || c.status != "applied" || c.method != "editorial" || c.text != "" || !c.effective.Equal(gazette) {
			t.Errorf("%v: %+v", k, c)
		}
		vs := buildVersions("نص المادة كما ينشره المركز", textSource{}, []*change{c}, -1, false)
		if len(vs) != 2 || vs[1].state != "repealed" || !vs[1].from.Equal(gazette) || vs[0].source != "centre" {
			t.Errorf("%v: versions %+v", k, vs)
		}
	}
	if got[0].law != 244611 || len(got[0].reasons) != 0 || got[0].resolvedBy != "by hand" {
		t.Errorf("the pattern pass's change was not taken over: %+v", got[0])
	}
	if len(byTarget) != 6 {
		t.Errorf("%d changes from Law 68/2026 article 4, want 6", len(byTarget))
	}
	for _, n := range notes {
		t.Errorf("note: %s", n)
	}
}
