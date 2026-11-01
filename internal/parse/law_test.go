package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests read real pages from the git-tracked archive, so they check the
// parser against what the Centre actually serves.
func fixture(t *testing.T, typ string, id int) []byte {
	t.Helper()
	p := filepath.Join("..", "..", "archive", typ, pad(id/1000), itoa(id)+".html")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Skipf("fixture %s not in the archive yet: %v", p, err)
	}
	return b
}

func TestCodeOfObligations(t *testing.T) {
	l, err := ParseLaw(244226, fixture(t, "law", 244226))
	if err != nil {
		t.Fatal(err)
	}
	if l.Title != "قانون الموجبات والعقود" || l.Kind != "قانون" || l.Repealed {
		t.Errorf("identity: %q %q repealed=%v", l.Title, l.Kind, l.Repealed)
	}
	if l.Number != nil {
		t.Errorf("printed رقم 0 should be no number, got %q", *l.Number)
	}
	if l.IssuedOn == nil || l.IssuedOn.Format("2006-01-02") != "1932-03-09" {
		t.Errorf("issued on %v", l.IssuedOn)
	}
	if l.GazetteNumber == nil || *l.GazetteNumber != "2642" || l.GazetteDate.Format("2006-01-02") != "1932-04-11" {
		t.Errorf("gazette %v %v", l.GazetteNumber, l.GazetteDate)
	}

	x, err := ParseLawText(fixture(t, "lawtext", 244226))
	if err != nil {
		t.Fatal(err)
	}
	if x.PrintedCount == nil || len(x.Articles) != *x.PrintedCount || len(x.Articles) != 1107 {
		t.Fatalf("articles: parsed %d, page says %v", len(x.Articles), x.PrintedCount)
	}
	first, last := x.Articles[0], x.Articles[len(x.Articles)-1]
	if first.Number != "1" || !strings.HasPrefix(first.TextAr, "الموجب هو رابطة قانونية") {
		t.Errorf("article 1: %q %q", first.Number, first.TextAr)
	}
	if first.TextFr == nil || !strings.HasPrefix(*first.TextFr, "Article Premier") {
		t.Errorf("article 1 French: %v", first.TextFr)
	}
	if last.Number != "1107" || !strings.Contains(last.TextAr, "بثلاثين شهرا") || last.TextFr == nil ||
		!strings.Contains(*last.TextFr, "trente mois") {
		t.Errorf("article 1107: %q | %q | %v", last.Number, last.TextAr, last.TextFr)
	}
	for _, a := range x.Articles {
		if strings.Contains(a.TextAr, "Art.") || strings.Contains(a.TextAr, "الكتاب الاول") {
			t.Errorf("article %s Arabic text holds French or a heading: %q", a.Number, a.TextAr)
			break
		}
		if a.TextAr == "" {
			t.Errorf("article %s has no Arabic text", a.Number)
		}
	}
	if len(x.Sections) < 50 || x.Sections[0].Depth != 0 || x.Sections[0].ParentID != nil {
		t.Errorf("sections: %d, first %+v", len(x.Sections), x.Sections[0])
	}
	// الكتاب الاول sits under القسم الاول? No: it is the next top-level part's
	// child in the Centre's tree; what matters is that nesting is consistent.
	for _, s := range x.Sections {
		if s.ParentID != nil && *s.ParentID == s.ID {
			t.Errorf("section %d is its own parent", s.ID)
		}
	}
	if x.Closing == nil || !strings.Contains(*x.Closing, "شارل دباس") {
		t.Errorf("closing: %v", x.Closing)
	}
	if x.Articles[1].SectionID == nil || *x.Articles[1].SectionID != 261591 {
		t.Errorf("article 2 should sit in section 261591, got %v", x.Articles[1].SectionID)
	}
}

func TestSmallDecree(t *testing.T) {
	x, err := ParseLawText(fixture(t, "lawtext", 333711))
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Articles) != 2 || !strings.Contains(x.Articles[0].TextAr, "ميشال ايليا عيسى") || x.Articles[0].TextFr != nil {
		t.Fatalf("got %d articles: %+v", len(x.Articles), x.Articles)
	}
}

func TestRepealedLaw(t *testing.T) {
	l, err := ParseLaw(244014, fixture(t, "law", 244014))
	if err != nil {
		t.Fatal(err)
	}
	if !l.Repealed || strings.Contains(l.Title, "ملغى") {
		t.Errorf("repealed=%v title=%q", l.Repealed, l.Title)
	}
	if l.Kind != "قانون منفذ بمرسوم" || l.Number == nil || *l.Number != "188" {
		t.Errorf("kind %q number %v", l.Kind, l.Number)
	}
	if l.RepealedBy == nil || l.RepealedBy.LawID != 244138 || !strings.Contains(l.RepealedBy.Label, "قانون الاستملاك") {
		t.Errorf("repealed by: %+v", l.RepealedBy)
	}
}

func pad(n int) string {
	s := itoa(n)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestArticleNumbers(t *testing.T) {
	for in, want := range map[string][2]string{
		"9 (عدلت بموجب قرار وسيط 13475 / 2022 )":                                  {"9", "(عدلت بموجب قرار وسيط 13475 / 2022 )"},
		"8 - مكرر ( اضيفت بموجب: قرار وسيط 11512 / 2013 ) (عدلت بموجب قرار وسيط)": {"8 مكرر", "( اضيفت بموجب: قرار وسيط 11512 / 2013 ) (عدلت بموجب قرار وسيط)"},
		"14 - مكرر2 ( اضيفت )": {"14 مكرر 2", "( اضيفت )"},
		"14 مادة ملغاة (الغيت بموجب قرار وسيط 13531 / 2023 )": {"14", "مادة ملغاة (الغيت بموجب قرار وسيط 13531 / 2023 )"},
		"1 - اصدار": {"1 اصدار", ""},
		"وحيدة":     {"وحيدة", ""},
		// Sub-numbered articles: "12 - 1" and "586 - (1)" are articles of
		// their own, numbered as they are cited.
		"12 - 1 ( أضيفت بموجب : 3 / 2025 )":      {"12-1", "( أضيفت بموجب : 3 / 2025 )"},
		"586 - (1) ( أضيفت بموجب : 164 / 2011 )": {"586-1", "( أضيفت بموجب : 164 / 2011 )"},
		"370 - (3)": {"370-3", ""},
		// The Centre's markup drops the start of some repeal notes, leaving
		// "173 /1948 )"; its own article page reads "(مادة ملغاة) (تشريع
		// الالغاء 0 /1948)".
		"173 /1948 )": {"173", "مادة ملغاة (تشريع الالغاء 0 /1948)"},
		"48 /1962)":   {"48", "مادة ملغاة (تشريع الالغاء 0 /1962)"},
		"324 /1952) (عدلت بموجب قانون 0/1948 )": {"324", "مادة ملغاة (تشريع الالغاء 0 /1952) (عدلت بموجب قانون 0/1948 )"},
	} {
		num, note := articleNumber(in)
		got := ""
		if note != nil {
			got = *note
		}
		if num != want[0] || got != want[1] {
			t.Errorf("%q: got %q / %q", in, num, got)
		}
	}
}
