package cite

import "testing"

const art = "مادة 2: تعدل المادة الثامنة من المرسوم رقم 1274/2009 بحيث تصبح على الوجه الآتي: يترتب على المرخص لها ان تدفع"

func TestCheckQuote(t *testing.T) {
	var a answer
	a.Changes = append(a.Changes, struct {
		Action, Kind, Number, Date, Name, Article, Part, Quote, Why string
		Year                                                        int
		Self                                                        bool
	}{Action: "replace", Kind: "decree", Number: "1274/2009", Article: "8", Quote: "تعدل  المادة الثامنة من المرسوم رقم 1274/2009"})
	a.Changes = append(a.Changes, a.Changes[0])
	a.Changes[1].Quote = "تعدل المادة الثامنة من المرسوم رقم 1275/2009" // not the article's words
	got := check(art, a)
	if !got[0].Valid {
		t.Errorf("quote with extra spaces and hamza variants should be valid")
	}
	if got[1].Valid {
		t.Errorf("a quote that isn't in the article must not be valid")
	}
}

func TestTargetIDAndRef(t *testing.T) {
	tg := Target{Kind: "decree", Number: "1274/2009", Article: "8"}
	if id := tg.ID(); id != "lb/decree/1274/2009/art/8" {
		t.Errorf("ID = %q", id)
	}
	r := tg.Ref()
	if r.Num != 1274 || r.Year != 2009 || r.Kind != "decree" {
		t.Errorf("Ref = %+v", r)
	}
	tg = Target{Kind: "law", Number: "328", Date: "2001-08-02", Article: "73"}
	if id := tg.ID(); id != "lb/law/2001-08-02/328/art/73" {
		t.Errorf("ID = %q", id)
	}
	if r := tg.Ref(); r.Date.IsZero() || r.Year != 2001 {
		t.Errorf("Ref = %+v", r)
	}
}

func TestDateInName(t *testing.T) {
	r := Target{Kind: "law", Name: "قانون 17 شباط سنة 1928"}.Ref()
	if r.Date.IsZero() || r.Date.Year() != 1928 || r.Date.Month() != 2 || r.Date.Day() != 17 {
		t.Errorf("date in name not read: %+v", r)
	}
}
