package site

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testVocab() *vocab {
	return newVocab(map[string]int{"الاستاذ": 20, "شهاب": 30, "كنعان": 12, "كانون": 90, "التعدي": 40, "تعود": 60,
		"بناء": 50, "خالد": 70, "امير": 25, "الخارجيه": 80, "السير": 100})
}

func TestSplitNumbers(t *testing.T) {
	for _, c := range []struct {
		q     string
		num   string
		year  int
		words string
	}{
		{"513 هيئة إدارة السير", "513", 0, "هيئة إدارة السير"},
		{"تعيين في وزارة الخارجية 2024", "", 2024, "تعيين وزارة الخارجية"},
		{"٩٥٩ اصول محاكمات مدنيه", "959", 0, "اصول محاكمات مدنيه"},
		{"decree 513 of 2005", "513", 2005, "decree of"},
		{"مدنيه", "", 0, "مدنيه"},
	} {
		num, year, words := splitNumbers(c.q)
		if num != c.num || year != c.year || strings.Join(words, " ") != c.words {
			t.Errorf("splitNumbers(%q) = %q %d %q, want %q %d %q", c.q, num, year, strings.Join(words, " "), c.num, c.year, c.words)
		}
	}
}

func TestArabiziSkeleton(t *testing.T) {
	for in, want := range map[string]string{"ta3adi": "تعد", "bina2": "بنء", "Shehab": "شهب", "Chehab": "شهب",
		"khaled": "خلد", "7ob": "حب"} {
		if got := arabiziSkeleton(in); got != want {
			t.Errorf("arabiziSkeleton(%q) = %q, want %q", in, got, want)
		}
	}
	if got, want := arSkeleton("البناء"), "بنء"; got != want {
		t.Errorf("arSkeleton(البناء) = %q, want %q", got, want)
	}
}

func TestLatinWords(t *testing.T) {
	v := testVocab()
	for in, want := range map[string]string{
		"Shehab":               "شهاب",
		"Chehab":               "شهاب",
		"kenaan":               "كنعان",
		"L'emir khaled":        "امير خالد",
		"al ta3adi fi l bina2": "التعدي بناء",
		"zzzz":                 "",
	} {
		if got := latinWords(in, v); got != want {
			t.Errorf("latinWords(%q) = %q, want %q", in, got, want)
		}
	}
	if got := latinWords("Shehab", nil); got != "" {
		t.Errorf("without a vocabulary latinWords = %q, want nothing", got)
	}
}

func TestVocabNearest(t *testing.T) {
	v := testVocab()
	if got, ok := v.nearest("الاستلذ"); !ok || got != "الاستاذ" {
		t.Errorf("nearest(الاستلذ) = %q %v, want الاستاذ", got, ok)
	}
	if got, ok := v.nearest("مدنية"); ok {
		t.Errorf("nearest of an unrelated word = %q, want none", got)
	}
	if got, ok := v.nearest("السير"); !ok || got != "السير" {
		t.Errorf("a known word is its own nearest, got %q %v", got, ok)
	}
}

func TestParseArticleRefArabicIndic(t *testing.T) {

	n, words, ok := parseArticleRef(arDigits.Replace("٩٥٩ اصول محاكمات مدنيه"))
	if !ok || n != "959" || strings.Join(words, " ") != "اصول محاكمات مدنيه" {
		t.Errorf("parseArticleRef = %q %q %v", n, words, ok)
	}
}

func TestZeroResultQueriesNowFind(t *testing.T) {
	dsn := os.Getenv("LEBLEG_DSN")
	if dsn == "" {
		t.Skip("LEBLEG_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("no database: %v", err)
	}
	defer pool.Close()
	if pool.Ping(ctx) != nil {
		t.Skip("database not reachable")
	}
	s := &Server{db: pool}
	v, err := loadVocab(ctx, pool)
	if err != nil {
		t.Skipf("no legislation table: %v", err)
	}
	s.vocab.Store(v)

	if to := s.articleRef(ctx, arDigits.Replace("٩٥٩ اصول محاكمات مدنيه")); !strings.HasSuffix(to, "/article/959/") {
		t.Errorf("articleRef for ٩٥٩ اصول محاكمات مدنيه = %q, want the article 959 address", to)
	}

	for _, c := range []struct{ q, via string }{
		{"513 هيئة إدارة السير", "هيئة"},
		{"civil marriage", "الزواج المدني"},
		{"cybercrime", "الجرائم المعلوماتية"},
		{"banking secrecy", "السرية المصرفية"},
		{"search and seizure", "التفتيش"},
		{"penalty", "عقوبة"},
		{"Shehab", "شهاب"},
		{"Chehab", "شهاب"},
		{"kenaan", "كنعان"},
		{"L'emir khaled", "خالد"},
		{"al ta3adi fi l bina2", "التعدي"},
		{"ساعات عمل الاستلذ في المدارس الثانوية", "الاستاذ"},
		{"تعيين في وزارة الخارجية 2024", "2024"},
		{"homosexual", "مجامعة"},
	} {
		res := Results{Q: arDigits.Replace(c.q)}
		if err := s.find(ctx, &res); err != nil {
			t.Fatal(err)
		}
		how := "plain"
		if !found(res) {
			alt, h, err := s.rescue(ctx, res.Q)
			if err != nil {
				t.Fatal(err)
			}
			res, how = alt, h
		}
		if !found(res) {
			t.Errorf("%q still finds nothing", c.q)
			continue
		}
		if how != "plain" && !strings.Contains(res.Via, c.via) {
			t.Errorf("%q was read as %q, want it to contain %q (via %s)", c.q, res.Via, c.via, how)
		}
	}

	res, how, err := s.rescue(ctx, "تعيين في وزارة الخارجية 2024")
	if err != nil || how == "" {
		t.Fatalf("year query: %v %q", err, how)
	}
	for _, l := range res.Laws {
		if l.IssuedOn == nil || l.IssuedOn.Year() != 2024 {
			t.Errorf("year query returned a text of %v", l.IssuedOn)
		}
	}

	if _, how, _ := s.rescue(ctx, "zzzz qqqq"); how != "" {
		t.Errorf("nonsense was rescued via %s", how)
	}
}
