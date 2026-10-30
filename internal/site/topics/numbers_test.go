package topics

import "testing"

func TestNumbers(t *testing.T) {
	cases := []struct {
		in     string
		strict bool
		want   []string
		not    []string
	}{
		{"fine of 2,250,000 pounds", true, []string{"2250000"}, nil},
		{"إجازة سنوية خمسة عشر يوما", true, []string{"15"}, []string{"5", "10"}},
		{"إجازة سنوية خمسة عشر يوما", false, []string{"15", "5", "10"}, nil},
		{"للاجير البالغ من العمر ستين عاما او له خمس وعشرون سنة", true, []string{"60", "25"}, []string{"5"}},
		{"المادة ٧٢ تاريخ ٢٥/١٠/٢٠٠٣", true, []string{"72", "25", "10", "2003"}, nil},
		{"fifteen days; twenty-five years; two hundred and five", true, []string{"15", "25", "205"}, []string{"20", "5"}},
		{"no one may; one month", true, nil, []string{"1"}},
		{"within two years", true, []string{"2"}, nil},
		{"مدة سنتين وثلاثة اشهر", true, []string{"2", "3"}, nil},
		{"اثنتي عشرة ساعة", true, []string{"12"}, []string{"2", "10"}},
		{"ألف وخمسمئة", false, []string{"1000"}, nil},
		{"غرامة من مئة الف الى مئة وخمسين الف ليرة", false, []string{"100000"}, nil},
		{"من ثلاثة ملايين ليرة", true, []string{"3000000"}, nil},
		{"100,000 pounds", true, []string{"100000"}, []string{"100.", "100"}},
		{"من ماية مليون الى ماية وخمسين مليون ليرة", false, []string{"100000000", "150000000", "150"}, nil},
		{"typing a page costs 1,500 LBP", true, []string{"1500"}, []string{"1.5"}},
		{"7.5%", true, []string{"7.5", "75"}, nil},
	}
	for _, c := range cases {
		got := Numbers(c.in, c.strict)
		for _, w := range c.want {
			if !got[w] {
				t.Errorf("Numbers(%q, strict=%v) lacks %s: %v", c.in, c.strict, w, got)
			}
		}
		for _, n := range c.not {
			if got[n] {
				t.Errorf("Numbers(%q, strict=%v) has %s: %v", c.in, c.strict, n, got)
			}
		}
	}
}

func TestArabicHundreds(t *testing.T) {
	got := Numbers("بين ثلاثماية وخمسين ألف وأربعماية وخمسين ألف ليرة، ومايتي ألف، وخمسماية ألف", false)
	for _, w := range []string{"350000", "450000", "200000", "500000"} {
		if !got[w] {
			t.Errorf("lacks %s: %v", w, got)
		}
	}
}
