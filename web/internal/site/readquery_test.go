package site

import (
	"strings"
	"testing"
)

func TestReadQuery(t *testing.T) {
	for _, c := range []struct {
		q                   string
		num, year           int
		date, kind, court   string
		lawCite, rulingCite bool
	}{
		{q: "رقم 28 تاريخ 10/2/2017", num: 28, year: 2017, date: "2017-02-10", lawCite: true},
		{q: "القانون رقم 175 تاريخ 8/5/2020", num: 175, year: 2020, date: "2020-05-08", kind: "law", lawCite: true},
		{q: "قانون رقم 41 24/11/2015", num: 41, year: 2015, date: "2015-11-24", kind: "law", lawCite: true},
		{q: "قانون الجنسية 31/1/1946", year: 1946, date: "1946-01-31", kind: "law", lawCite: true},
		{q: "3/12/1951", year: 1951, date: "1951-12-03", lawCite: true},
		{q: `قرار رقم 513 تاريح 11\7\2005`, num: 513, year: 2005, date: "2005-07-11", kind: "decision", lawCite: true},
		{q: "قرار رقم 513/2005", num: 513, year: 2005, kind: "decision", lawCite: true},
		{q: "décret 11614 de 1969", num: 11614, year: 1969, kind: "decree", lawCite: true},
		{q: "décret n° 11614 de 1969", num: 11614, year: 1969, kind: "decree", lawCite: true},
		{q: "décret 11614", num: 11614, kind: "decree", lawCite: true},
		{q: "مرسوم اشتراعي 1306", num: 1306, kind: "decreelaw", lawCite: true},
		{q: "المرسوم 398 تاريخ 29/11/1949", num: 398, year: 1949, date: "1949-11-29", kind: "decree", lawCite: true},
		{q: "القرار رقم 122/ل.ر تاريخ 19/6/1939", num: 122, year: 1939, date: "1939-06-19", kind: "hc", lawCite: true},

		{q: "القرار رقم 123/ل.ر", num: 123, kind: "hc", lawCite: true},
		{q: "قرار المفوض السامي رقم 226", num: 226, kind: "hc", lawCite: true},
		{q: "arrêté 16 L.R. de 1935", num: 16, year: 1935, kind: "hc", lawCite: true},
		{q: "قوانين الجنسية", kind: "law"},
		{q: "مراسيم التجنيس", kind: "decree"},
		{q: "The Forest Law issued on 07/01/1949", year: 1949, date: "1949-01-07", kind: "law", lawCite: true},
		{q: "قرار 122/ 1939", num: 122, year: 1939, kind: "decision", lawCite: true},
		{q: "décret-loi n.34 du 4 aout 1967", num: 34, year: 1967, date: "1967-08-04", kind: "decreelaw", lawCite: true},
		{q: "قرار رقم 50/2002 تميز جزائي ، غرفة سادسة ، الرئيس رالف رياشي", num: 50, year: 2002, kind: "decision", court: "تمييز جزائي", rulingCite: true},
		{q: "محكمة التمييز الجزائية 2006", year: 2006, court: "تمييز جزائي", rulingCite: true},

		{q: "تمييز جزائي غرفة 2 قرار رقم 93/33تاريخ 19/1/1993", num: 33, year: 1993, date: "1993-01-19", kind: "decision", court: "تمييز جزائي", rulingCite: true},
		{q: "قرار رقم 33/93 تاريخ 19/1/1993 تمييز جزائي", num: 33, year: 1993, date: "1993-01-19", kind: "decision", court: "تمييز جزائي", rulingCite: true},

		{q: "decree 9341 17 nov 2012", num: 9341, year: 2012, date: "2012-11-17", kind: "decree", lawCite: true},
		{q: "Decree 10897, Decision 1/1 (Food/Beverage Warehouses)", num: 10897, kind: "decree", lawCite: true},

		{q: "قانون الجنسية اللبنانية 1946", year: 1946, kind: "law"},
		{q: "337", num: 337},
		{q: "1930", year: 1930},
		{q: "قانون السير", kind: "law"},
		{q: "حق الوصول الى معلومات"},
	} {
		r := readQuery(c.q)
		date := ""
		if !r.Date.IsZero() {
			date = r.Date.Format("2006-01-02")
		}
		if r.Num != c.num || r.Year != c.year || date != c.date || r.Kind != c.kind || strings.Join(r.Court, " ") != c.court ||
			r.lawCite() != c.lawCite || r.rulingCite() != c.rulingCite {
			t.Errorf("readQuery(%q) = num %d year %d date %q kind %q court %q cite %v/%v; want %d %d %q %q %q %v/%v",
				c.q, r.Num, r.Year, date, r.Kind, strings.Join(r.Court, " "), r.lawCite(), r.rulingCite(),
				c.num, c.year, c.date, c.kind, c.court, c.lawCite, c.rulingCite)
		}
	}
}

func TestReadQueryWords(t *testing.T) {
	r := readQuery("تنظيم وزارة الخارجية مرسوم 1306 تاريخ 18/6/1971")
	if got := strings.Join(r.Words, " "); got != "تنظيم وزارة الخارجية" {
		t.Errorf("words = %q", got)
	}
	if got := readQuery("رقم 28 تاريخ 10/2/2017").label(); got != "28 10/02/2017" {
		t.Errorf("label = %q", got)
	}
	if got := readQuery("قرار رقم 513/2005").label(); got != "513/2005" {
		t.Errorf("label = %q", got)
	}
	if !readQuery("31/2/1946").Date.IsZero() {
		t.Error("31 February read as a date")
	}
	if got := strings.Join(readQuery("قوانين الجنسية و تعديلاتها").Words, " "); got != "الجنسية" {
		t.Errorf("words = %q", got)
	}
	if got := strings.Join(readQuery("القرار رقم 123/ل.ر").Words, " "); got != "" {
		t.Errorf("L.R. left in the words: %q", got)
	}
}

func TestTidyQuery(t *testing.T) {
	for _, c := range []struct {
		q, want string
		cut     bool
	}{
		{"القانون30 14/9/1982", "القانون 30 14/9/1982", false},
		{"المرسوم 11244تاريخ 25/10/2003", "المرسوم 11244 تاريخ 25/10/2003", false},
		{"•\tالقرار رقم 45 تاريخ 16/02/1967،", "القرار رقم 45 تاريخ 16/02/1967", false},
		{"قانون الغاباتت الصادر بتاريخ 7/1/1949 عدد الجريدة الرسمية 3 تاريخ النشر  19/1/1949 الصفحة 30-45", "قانون الغاباتت الصادر بتاريخ 7/1/1949", true},
		{"The Forest Law issued on 07/01/1949, Official Gazette issue: 3 | Date of publication: 19/01/1949 | Pages: 30–54", "The Forest Law issued on 07/01/1949", true},
		{"loi du 4 aout 1967 JO n 33", "loi du 4 aout 1967", true},
		{"قانون الجريدة الرسمية", "قانون الجريدة الرسمية", false},
		{"القانون ٣٣٧", "القانون 337", false},
		{"قرار رقم 122/ل.ر", "قرار رقم 122/ل.ر", false},
		{"15-S", "15-S", false},
	} {
		got, cut := tidyQuery(c.q)
		if got != c.want || cut != c.cut {
			t.Errorf("tidyQuery(%q) = %q %v; want %q %v", c.q, got, cut, c.want, c.cut)
		}
	}
}
