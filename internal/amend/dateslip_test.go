package amend

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func slipRef(kind string, num int, date time.Time) Ref {
	return Ref{Kind: kind, Num: num, Number: strconv.Itoa(num), Date: date, Year: date.Year()}
}

func TestDateSlip(t *testing.T) {
	before := day(2002, 12, 23)
	laws := []Law{
		{ID: 244626, Kind: "قانون منفذ بمرسوم", Number: "10830", Issued: day(1962, 10, 9), Gazette: day(1962, 10, 17), Title: "الجمعيات"},
		{ID: 193620, Kind: "مرسوم", Number: "10830", Issued: day(2003, 9, 6), Title: "مرسوم"},
		{ID: 229971, Kind: "قرار", Number: "10830", Issued: day(1962, 10, 12), Title: "قرار بالرقم نفسه"},
		{ID: 244971, Kind: "قانون منفذ بمرسوم", Number: "13955", Issued: day(1963, 9, 26), Title: "نص"},
		// two candidates in 1970
		{ID: 1, Kind: "مرسوم", Number: "500", Issued: day(1970, 1, 5), Title: "أ"},
		{ID: 2, Kind: "قانون", Number: "500", Issued: day(1970, 6, 5), Title: "ب"},
		// a decree-law is not a decree
		{ID: 3, Kind: "مرسوم اشتراعي", Number: "77", Issued: day(1959, 3, 3), Title: "ج"},
	}
	ix := NewIndex(laws)
	cited := day(1962, 10, 19)

	// Off by default.
	if id, _ := ix.Resolve(slipRef(KindDecree, 10830, cited), before); id != 0 {
		t.Errorf("off by default, resolved to %d", id)
	}
	ix.AllowDateSlip()

	// A decree or a law-by-decree "of 19/10/1962" is the law-by-decree of 9/10/1962, a decision of the same year does not count against it.
	id, how := ix.Resolve(slipRef(KindDecree, 10830, cited), before)
	if id != 244626 || how != "date in the citation differs: cited 1962-10-19, text dated 1962-10-09" {
		t.Errorf("decree: %d %q", id, how)
	}
	for _, kind := range []string{KindLawByDecree, KindLaw} { // the year already settles these
		if id, _ := ix.Resolve(slipRef(kind, 10830, cited), before); id != 244626 {
			t.Errorf("%s: %d", kind, id)
		}
	}
	if id, how := ix.Resolve(slipRef(KindDecree, 13955, day(1963, 9, 16)), before); id != 244971 || !strings.Contains(how, "cited 1963-09-16, text dated 1963-09-26") {
		t.Errorf("13955: %d %q", id, how)
	}

	// Never across years.
	if id, _ := ix.Resolve(slipRef(KindDecree, 10830, day(1963, 10, 19)), before); id != 0 {
		t.Errorf("another year resolved to %d", id)
	}
	// Never for a decision, nor for a citation of no kind.
	if id, _ := ix.Resolve(slipRef(KindDecision, 13955, day(1963, 9, 16)), before); id != 0 {
		t.Errorf("a decision resolved to %d", id)
	}
	if id, how := ix.dateSlip(slipRef("", 13955, day(1963, 9, 16))); id != 0 {
		t.Errorf("no kind resolved to %d %q", id, how)
	}
	// Never when two texts qualify.
	if id, _ := ix.Resolve(slipRef(KindLawByDecree, 500, day(1970, 3, 3)), before); id != 0 {
		t.Errorf("two candidates resolved to %d", id)
	}
	// Kinds that do not go together.
	if id, _ := ix.Resolve(slipRef(KindDecree, 77, day(1959, 3, 9)), before); id != 0 {
		t.Errorf("a decree cited for a decree-law resolved to %d", id)
	}
	if id, _ := ix.Resolve(slipRef(KindDecreeLaw, 77, day(1959, 3, 9)), before); id != 3 {
		t.Errorf("a decree-law cited for a decree-law: %d", id)
	}
	// A date that fits a text is not a slip: the exact match wins.
	if id, how := ix.Resolve(slipRef(KindLawByDecree, 10830, day(1962, 10, 9)), before); id != 244626 || strings.Contains(how, "differs") {
		t.Errorf("exact date: %d %q", id, how)
	}
}
