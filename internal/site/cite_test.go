package site

import (
	"strings"
	"testing"
)

func TestParseCitation(t *testing.T) {
	for _, c := range []struct {
		q    string
		n    string
		y    int
		want bool
	}{
		{"طوارئ العمل - مرسوم اشتراعي رقم 136 تاريخ 16/09/1983", "136", 1983, true},
		{"قانون 337 تاريخ 8-6-1994", "337", 1994, true},
		{"قانون 337 تاريخ 8-6-1994 اصول ممارسة كتابة العدل'", "337", 1994, true},
		{"القرار ٥١٣/٢٠٠٥", "513", 2005, true},
		{"القرار 513/2005", "513", 2005, true},
		{"decree no. 136 dated 16/09/1983", "136", 1983, true},
		{"المرسوم رقم 3173", "", 0, false},
		{"تعديل الغرامات 2024", "", 0, false},
		{"16/09/1983", "", 0, false},
		{"decret-loi n.34 du 4 aout 1967", "34", 1967, true},
		{"المرسوم الاشتراعي 34 العائد الى 4 اب 1967", "34", 1967, true},
		{"loi du 4 aout 1967", "", 0, false},
	} {
		n, y, ok := parseCitation(c.q)
		if n != c.n || y != c.y || ok != c.want {
			t.Errorf("%q = %q %d %v", c.q, n, y, ok)
		}
	}
}

func TestParseArticleRef(t *testing.T) {
	for _, c := range []struct {
		q     string
		n     string
		words string
		want  bool
	}{
		{"الماده ٢٤٨ موجبات وعقود", "248", "موجبات وعقود", true},
		{"المادة 216 ملكية عقارية", "216", "ملكية عقارية", true},
		{"216 من قانون الملكية العقارية", "216", "الملكية العقارية", true},
		{"article 248 code of obligations", "248", "obligations", true},
		{"المادة 248", "248", "", false},
		{"959 اصول محاكمات مدنيه", "959", "اصول محاكمات مدنيه", true},
		{"677 موجبات وعقود", "677", "موجبات وعقود", true},
		{"959", "", "", false},
		{"قانون 337 تاريخ 8-6-1994", "", "", false},
	} {
		n, w, ok := parseArticleRef(c.q)
		if n != c.n || strings.Join(w, " ") != c.words || ok != c.want {
			t.Errorf("%q = %q %v %v", c.q, n, w, ok)
		}
	}
}
