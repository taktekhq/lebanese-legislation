package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuling(t *testing.T) {
	r, ok, err := ParseRuling(150262, fixture(t, "ruling", 150262))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if *r.Court != "تمييز جزائي" || *r.Number != "240" || *r.Year != 2020 || r.DecidedOn.Format("2006-01-02") != "2020-12-30" {
		t.Errorf("identity: %v %v %v %v", *r.Court, *r.Number, *r.Year, r.DecidedOn)
	}
	if *r.President != "سهير الحركة" || len(r.Members) != 2 || r.Members[0] != "ابو خاطر" {
		t.Errorf("panel: %v %v", *r.President, r.Members)
	}
	if len(r.Cites) == 0 || r.Cites[0].LawID != 244611 || *r.Cites[0].ArticleCentreID != 984632 || *r.Cites[0].ArticleNumber != "565" {
		t.Errorf("cites: %+v", r.Cites)
	}
	if len(r.TextAr) < 50 || strings.Contains(r.TextAr, "إتصل بنا") {
		t.Errorf("text: %q", r.TextAr)
	}
}

func TestGazette(t *testing.T) {
	g, ok, err := ParseGazette(12000, fixture(t, "journal", 12000))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if g.Entries[0].LawID != 170266 || *g.Entries[0].Pages != "2513" || g.Entries[1].LawID != 170267 {
		t.Errorf("entries: %+v", g.Entries[:2])
	}
}

// The Centre's pages 70390 and 70392 (internal/parse/testdata, copied from
// archive/ruling/0070) both give number 0, year 0 and 01/01/1900 with no
// text. 70390 links its own judgment PDF (RulingFile.aspx?RuliID=70390),
// which we hold; 70392 links none and stays a blank record.
func TestRulingWithOnlyItsFile(t *testing.T) {
	read := func(id int) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", "ruling", itoa(id)+".html"))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	r, ok, err := ParseRuling(70390, read(70390))
	if err != nil || !ok {
		t.Fatalf("70390 links its PDF, want a ruling: ok=%v err=%v", ok, err)
	}
	if r.Court == nil || *r.Court != "إستئناف مدني" || r.President == nil || *r.President != "كلود كرم" {
		t.Errorf("70390 identity: %v %v", r.Court, r.President)
	}
	if r.Number != nil || r.Year != nil || r.DecidedOn != nil || r.TextAr != "" {
		t.Errorf("70390 placeholders must load as null: %v %v %v %q", r.Number, r.Year, r.DecidedOn, r.TextAr)
	}
	if _, ok, err := ParseRuling(70392, read(70392)); err != nil || ok {
		t.Errorf("70392 has no text, date or file, want blank: ok=%v err=%v", ok, err)
	}
	// A link to another ruling's file does not make a blank record a ruling.
	if _, ok, _ := ParseRuling(70391, read(70390)); ok {
		t.Error("70390's link must not count for ruling 70391")
	}
}
