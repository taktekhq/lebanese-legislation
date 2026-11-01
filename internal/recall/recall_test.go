package recall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Hand-counted fixture: 20 articles. Counted by eye, 10 open with a verb of
// the amend list (articles 0-7 and 9 with يعدل/تلغى/يضاف/تستبدل/تعدل, and 17,
// "عدلت بموجب", which the list holds); article 8 has تعدل only after a heading,
// and the other 9 are ordinary text. Rows exist for articles 0-7, so the
// ratio is 8/10 = 0.8 with two misses, articles 9 and 17.
func fixture() ([]Article, map[Key]bool) {
	var a []Article
	add := func(n int, t string) {
		a = append(a, Article{LawID: 1, Number: string(rune('0'+n%10)) + string(rune('a'+n)), Text: t})
	}
	for i, t := range []string{
		"تعدل المادة 5 من القانون",
		"يعدل نص المادة 6",
		"تلغى المادة 7 من القانون",
		"يضاف الى القانون مادة جديدة",
		"تستبدل المادة 9 بالنص الآتي",
		"1- تعدل المادة 10 من القانون",
		"يعدل البند 2 من المادة 11",
		"تلغى احكام المادة 12",
		"المادة الثانية: تعدل المادة 4", // verb mid-article, not a clause opener
		"يعدل الحد الاقصى للرواتب",
	} {
		add(i, t)
	}
	for i, t := range []string{
		"يعمل بهذا القانون فور نشره",
		"تحدد المهل بمرسوم",
		"تنشر هذه القرارات في الجريدة الرسمية",
		"يحدد مجلس الوزراء الاصول",
		"لا يجوز الجمع بين وظيفتين",
		"تطبق احكام هذا القانون على الجميع",
		"المواد التالية تحدد الرسوم",
		"عدلت بموجب القانون 13/2021",
		"الغي هذا النص",
		"تاريخ العمل بالقانون",
	} {
		add(10+i, t)
	}
	rows := map[Key]bool{}
	for _, i := range []int{0, 1, 2, 3, 4, 5, 6, 7} {
		rows[Key{1, a[i].Number}] = true
	}
	return a, rows
}

func TestCountByHand(t *testing.T) {
	a, rows := fixture()
	r := Count(a, rows)
	if r.Candidates != 10 || r.Produced != 8 {
		t.Fatalf("candidates %d produced %d, want 10 and 8", r.Candidates, r.Produced)
	}
	if got, want := r.Ratio, 0.8; got != want {
		t.Errorf("ratio %v, want %v", got, want)
	}
	if len(r.Groups) != 2 || r.Groups[0].Count != 1 || r.Groups[1].Count != 1 {
		t.Errorf("groups %+v", r.Groups)
	}
}

func TestComparePasses(t *testing.T) {
	a, rows := fixture()
	base := Count(a, rows)
	if why := Compare(base, base); why != "" {
		t.Errorf("same night failed: %s", why)
	}
	// A drop of 0.4 points is inside the margin.
	now := base
	now.Ratio = base.Ratio - 0.004
	if why := Compare(now, base); why != "" {
		t.Errorf("0.4 point drop failed: %s", why)
	}
}

// One heading pattern dropped: 30 articles with the same opening lose their rows.
func TestCompareDroppedPattern(t *testing.T) {
	var arts []Article
	rows := map[Key]bool{}
	for i := 0; i < 100; i++ {
		n := string(rune('A'+i%26)) + string(rune('a'+i/26))
		txt := "تعدل المادة 5 من القانون"
		if i%3 == 0 {
			txt = "يضاف الى القانون مادة جديدة"
		}
		arts = append(arts, Article{1, n, txt})
		rows[Key{1, n}] = true
	}
	base := Count(arts, rows)
	if base.Ratio != 1 {
		t.Fatalf("baseline ratio %v", base.Ratio)
	}
	for _, a := range arts {
		if strings.HasPrefix(a.Text, "يضاف") {
			delete(rows, Key{1, a.Number})
		}
	}
	now := Count(arts, rows)
	why := Compare(now, base)
	if why == "" {
		t.Fatal("a dropped pattern passed")
	}
	if !strings.Contains(why, "baseline 100.00%") || !strings.Contains(why, "34 articles") {
		t.Errorf("why = %q", why)
	}
}

func TestNewSmallGroupAlone(t *testing.T) {
	base := Report{Candidates: 1000, Produced: 900, Ratio: 0.9}
	now := Report{Candidates: 1000, Produced: 900, Ratio: 0.9, Groups: []Group{{"x y z", 25}}}
	if why := Compare(now, base); why != "" {
		t.Errorf("a group of 25 failed: %s", why)
	}
	now.Groups[0].Count = 26
	if Compare(now, base) == "" {
		t.Error("a new group of 26 passed")
	}
}

type fakeSrc struct {
	a    []Article
	rows map[Key]bool
	err  error
}

func (f fakeSrc) Load(context.Context) ([]Article, map[Key]bool, error) { return f.a, f.rows, f.err }

func TestRunExitCodes(t *testing.T) {
	a, rows := fixture()
	bp := filepath.Join(t.TempDir(), "recall-baseline.json")
	var o, e bytes.Buffer
	run := func(src Source, init bool) int {
		o.Reset()
		e.Reset()
		return Run(context.Background(), src, bp, init, &o, &e)
	}
	if c := run(fakeSrc{a, rows, nil}, false); c != 2 {
		t.Fatalf("no baseline: exit %d, want 2", c)
	}
	if c := run(fakeSrc{a, rows, nil}, true); c != 0 {
		t.Fatalf("init: exit %d: %s", c, e.String())
	}
	if c := run(fakeSrc{a, rows, nil}, false); c != 0 {
		t.Fatalf("same night: exit %d: %s", c, e.String())
	}
	if c := run(fakeSrc{err: errors.New("down")}, false); c != 2 {
		t.Errorf("db down: exit %d, want 2", c)
	}
	// Two fewer rows: ratio 0.6 against 0.8.
	low := map[Key]bool{}
	for k := range rows {
		low[k] = true
	}
	delete(low, Key{1, a[0].Number})
	delete(low, Key{1, a[1].Number})
	if c := run(fakeSrc{a, low, nil}, false); c != 1 || !strings.Contains(e.String(), "baseline 80.00%") {
		t.Errorf("fall: exit %d, %q", c, e.String())
	}
	// A rise rewrites the baseline.
	rows[Key{1, a[9].Number}] = true
	if c := run(fakeSrc{a, rows, nil}, false); c != 0 {
		t.Fatalf("rise: exit %d", c)
	}
	b, _ := os.ReadFile(bp)
	if !strings.Contains(string(b), `"produced": 9`) {
		t.Errorf("baseline not raised: %s", b)
	}
	os.WriteFile(bp, []byte("{"), 0o644)
	if c := run(fakeSrc{a, rows, nil}, false); c != 2 {
		t.Errorf("bad baseline: exit %d, want 2", c)
	}
}
