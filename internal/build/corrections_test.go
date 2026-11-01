package build

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/db"
)

// The outside reader's comparison of 27 Oct 2026 (sections 1, 3 and 4):
// per article, the words the site showed and the words it should show, as
// the report quotes them.
var georges = []struct {
	law          int
	number       string
	wrong, right []string
}{
	{244586, "27", []string{"بالامور الواجر تسجيلها"}, []string{"بالامور الواجب تسجيلها"}},
	{244586, "42", []string{"اشهر المعطاة التصحيح"}, []string{"اشهر المعطاة للتصحيح"}},
	{244586, "45", []string{"في ما بينم "}, []string{"في ما بينهم"}},
	{244586, "95", []string{"غير قانوني يحب المساهمين"}, []string{"غير قانوني يحق للمساهمين"}},
	{244586, "115", []string{"وعندما يحصل الاستيراد"}, []string{"وعندما يحصل الاسترداد"}},
	{244586, "116", []string{"سائر المالكي."}, []string{"حق الرقبة الحق في حضور الجمعيات العمومية غير العادية", "ويعتبر صاحب", "سائر المالكين"}},
	{244586, "118", []string{"وفاقا المهلة", "ضرر فاحش عل المساهم"}, []string{"وفاقا للمهلة", "ضرر فاحش على المساهم"}},
	{244586, "145", []string{"الارباح الصافية وام بطريقة"}, []string{"الارباح الصافية واما بطريقة"}},
	{244586, "146", []string{"في حال وجود ارتفاق مخالف"}, []string{"في حال وجود اتفاق مخالف"}},
	{244586, "152", []string{"تأخذ بموجبه الشركة عاما باستقالة"}, []string{"تأخذ بموجبه الشركة علما باستقالة"}},
	{244586, "153", []string{"بعود لرئيس", "لا يستطيع معه القيام"}, []string{"يعود لرئيس", "لا يستطيع معها القيام"}},
	{244586, "154", []string{"عنوان تجاري واحد بعد بمثابة"}, []string{"عنوان تجاري واحد يعد بمثابة"}},
	{244586, "155", []string{"الاعمال الشركة"}, []string{"بدلا من رئيس مجلس الادارة", "اعمال الشركة"}},
	{244586, "157", []string{"دون ان تكون نلزمة"}, []string{"دون ان تكون ملزمة"}},
	{244586, "160", []string{"والمدير العام المساعد والمدير العام المساعد"}, []string{"والمدير العام والمدير العام المساعد"}},
	{244586, "167", []string{"ويوجه عام"}, []string{"وبوجه عام"}},
	{244586, "173", []string{"البدل المقرر لمفوض المراقبة"}, []string{"البدل المقرر لمفوضي المراقبة"}},
	{244586, "213", []string{"وإذا انفق على تاريخ آخر"}, []string{"وإذا اتفق على تاريخ آخر"}},
	{244586, "214", []string{"انعقدت فيه الجمعية بالمسبة"}, []string{"انعقدت فيه الجمعية بالنسبة"}},
	{244611, "489", nil, []string{"7 ـ أ ـ", "7 ـ ب ـ", "على ضرب زوجه أو إيذائه", "على تهديد زوجه"}},
	{244611, "316 مكرر", []string{"وبالاستناد الى الاتفاقية العربية لمكافحة الارهاب", "يعمل بهذا القانون فور نشره"},
		[]string{"كل من يقوم أو يحاول القيام", "من قانون العقوبات"}},
	{244611, "604", []string{"ثانياً:", "يعمل بهذا القانون فور نشره"}, []string{"من خالف القرارات", "فضلاً عن الغرامة."}},
}

// tsvRows reads a tab-separated file of law id, article number, wording.
func tsvRows(t *testing.T, raw string, cols int) map[[2]string]string {
	t.Helper()
	out := map[[2]string]string{}
	for _, ln := range strings.Split(raw, "\n") {
		f := strings.Split(ln, "\t")
		if strings.HasPrefix(ln, "#") || strings.TrimSpace(ln) == "" || f[0] == "law_id" {
			continue
		}
		if len(f) != cols {
			t.Fatalf("line %q: %d columns, want %d", ln, len(f), cols)
		}
		out[[2]string{f[0], f[1]}] = CorrectionText(f[2])
	}
	return out
}

func TestCorrectionsOfGeorgesReport(t *testing.T) {
	// The site's wording before (the amending laws as the Centre carries
	// them), built into versions and corrected as the build does.
	raw, err := os.ReadFile("testdata/corrections-2026-10-27.tsv")
	if err != nil {
		t.Fatal(err)
	}
	before := tsvRows(t, string(raw), 3)
	fixes := tsvRows(t, db.ArticleCorrections, 4)
	for _, g := range georges {
		k := [2]string{strconv.Itoa(g.law), g.number}
		site, ok := before[k]
		if !ok {
			t.Errorf("%v: no wording before", k)
			continue
		}
		fix, ok := fixes[k]
		if !ok {
			t.Errorf("%v: no row in article_corrections.tsv", k)
			continue
		}
		vs := [][]any{
			{g.law, g.number, 1, 0, nil, nil, "in_force", nil, "none", nil, nil, nil, nil},
			{g.law, g.number, 2, 0, nil, nil, "in_force", site, "amendment", 1, nil, SourceAmendment, "1/1"},
		}
		// The Centre's article reads as the row does for most: the row
		// still applies, over the amending law's wording.
		vs, skip := correct(vs, g.law, g.number, fix, "test", fix, textSource{SourceUL, "lawtext"})
		if skip != "" {
			t.Errorf("%v: skipped: %s", k, skip)
			continue
		}
		last := vs[len(vs)-1]
		if last[2] != 2 || last[3] != 1 || last[8] != "editorial" || last[12] != CorrectionSource(g.law, g.number) {
			t.Errorf("%v: latest version %v", k, last[:4])
		}
		got := last[7].(string)
		for _, w := range g.wrong {
			if !strings.Contains(site, w) {
				t.Errorf("%v: the site's wording never had %q", k, w)
			}
			if strings.Contains(got, w) {
				t.Errorf("%v: still has %q", k, w)
			}
		}
		for _, r := range g.right {
			if !strings.Contains(got, r) {
				t.Errorf("%v: lacks %q", k, r)
			}
		}
	}
}

func TestCorrectSkips(t *testing.T) {
	src := textSource{SourceUL, "lawtext"}
	// Unamended, and the Centre now reads the same.
	if _, skip := correct(nil, 1, "1", "نص", "test", "نص", src); skip == "" {
		t.Error("a row the Centre now reads applied")
	}
	// Unamended: the Centre's text becomes 1.0, the row 1.1.
	vs, skip := correct(nil, 1, "1", "نص", "test", "نض", src)
	if skip != "" || len(vs) != 2 || vs[0][7] != "نض" || vs[1][7] != "نص" || vs[1][3] != 1 {
		t.Errorf("unamended: %v %q", vs, skip)
	}
	// The latest version reads the same: nothing to add.
	if _, skip := correct(vs, 1, "1", "نص", "test", "نض", src); skip == "" {
		t.Error("a row the latest version reads applied")
	}
	// Repealed.
	rep := [][]any{{1, "1", 2, 0, nil, nil, "repealed", nil, "none", nil, nil, nil, nil}}
	if _, skip := correct(rep, 1, "1", "نص", "test", "نض", src); skip == "" {
		t.Error("a repealed article corrected")
	}
}
