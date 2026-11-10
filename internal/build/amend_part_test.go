package build

import (
	"testing"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/amend"
)

// The Centre's text of an article and one clause of it, as an amending law
// words that clause (the shape of the sharia courts law's article 17 after
// Law 768/2006, shortened: its items, then the one the law added).
const (
	centreArticle = "يدخل في اختصاص المحاكم الشرعية الدعاوى والمعاملات المتعلقة بالأمور الآتية:\n\n1 - الخطبة وهدايا الخطبة.\n\n2 - الزواج العقد.\n\n3 - المهر والجهاز.\n\n20 - الوقف الذري.\n\n21 ـ فيما يتعلق بسائر معاملات الوقف الخيري ووقف المساجد والمصليات، يشترط أخذ الموافقة المسبقة من المرجع الأعلى للأوقاف الإسلامية."
	addedClause   = "21 ـ فيما يتعلق بسائر معاملات الوقف الخيري ووقف المساجد والمصليات، يشترط أخذ الموافقة المسبقة من المرجع الأعلى للأوقاف الإسلامية."
)

func TestInside(t *testing.T) {
	cases := []struct {
		name, centre, wording string
		want                  bool
	}{
		{"a clause at the end of a longer article", centreArticle, addedClause, true},
		{"the opening of a longer article", centreArticle, "يدخل في اختصاص المحاكم الشرعية الدعاوى والمعاملات المتعلقة بالأمور الآتية:", true},
		{"the whole article, spelled another way", "تنشأ الجمعيات التعاونية بموجب قرار يصدر عن رئيس إدارة التعاون.", "تنشأ الجمعيات التعاونيه بموجب قرار يصدر عن رئيس ادارة التعاون", false},
		{"the whole article under the Centre's heading", "مادة 21 المعدلة: " + addedClause, addedClause, false},
		{"the Centre's short closing note", addedClause + "\n\nمرسوم تطبيقي لهذه المادة", addedClause, false},
		{"another sum: the Centre's text is not this wording", "يحدد بدل الأتعاب بمبلغ 40,000,000 ليرة لبنانية شهريا لكل محام معتمد لدى هيئة القضايا.", "يحدد بدل الأتعاب بمبلغ 60,000,000 ليرة لبنانية شهريا لكل محام معتمد", false},
		{"no wording", centreArticle, "", false},
	}
	for _, c := range cases {
		if got := inside(c.centre, c.wording); got != c.want {
			t.Errorf("%s: inside = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestShort(t *testing.T) {
	if !short(centreArticle, "21 ـ فيما يتعلق بسائر معاملات الوقف الخيري") {
		t.Error("a wording under half the article is short")
	}
	if short(centreArticle, centreArticle[:len(centreArticle)*3/4]) {
		t.Error("three quarters of the article is not short")
	}
	if short("", addedClause) {
		t.Error("no Centre text: nothing to be short of")
	}
}

// A wording that is a part of the article never becomes the article's
// version: the Centre's text does when it shows the change, and no wording
// stands when it does not.
func TestBuildVersionsPartIsNotTheArticle(t *testing.T) {
	day := time.Date(2006, 11, 16, 0, 0, 0, 0, time.UTC)
	centre := textSource{"ul", "lawtext/244292"}
	text := func(vs []version) (string, string) {
		for i := len(vs) - 1; i >= 0; i-- {
			if vs[i].text != "" {
				return vs[i].text, vs[i].source
			}
		}
		return "", ""
	}

	// Carried inside the Centre's longer text.
	c := &change{id: 1068, byLaw: 215361, byArticle: "1", action: amend.Replace, law: 244292, article: "17",
		text: addedClause, effective: day, status: "applied", inside: true}
	if got, src := text(buildVersions(centreArticle, centre, []*change{c}, 0, false)); got != centreArticle || src != "centre" {
		t.Errorf("inside: printed %q from %s, want the Centre's text", got, src)
	}

	// The same change read as whole: the old behaviour, still right for a
	// wording that is the whole article.
	c = &change{id: 1068, byLaw: 215361, byArticle: "1", action: amend.Replace, law: 244292, article: "17",
		text: addedClause, effective: day, status: "applied"}
	if got, src := text(buildVersions(addedClause, centre, []*change{c}, 0, false)); got != addedClause || src != "amendment" {
		t.Errorf("whole: printed %q from %s, want the amendment's wording", got, src)
	}

	// Newer than the Centre's text and only a part: the Centre's text
	// stays printed, the change waits as a note.
	c = &change{id: 3363, byLaw: 329388, byArticle: "1", action: amend.Replace, law: 244971, article: "9",
		part: "البند السادس فقره (4) (ب)", text: addedClause, effective: day, status: "partial"}
	vs := buildVersions(centreArticle, centre, []*change{c}, -1, false)
	if got, src := text(vs); got != centreArticle || src != "centre" {
		t.Errorf("part, newer: printed %q from %s, want the Centre's text", got, src)
	}
	if last := vs[len(vs)-1]; last.text != "" || last.amendment != 3363 {
		t.Errorf("part, newer: the change's version has wording %q (amendment %d), want none", last.text, last.amendment)
	}

	// A quoted old wording of a part does not fill the version before it.
	c = &change{id: 1, byLaw: 2, byArticle: "1", action: amend.Replace, law: 3, article: "2",
		text: "تمتد فترة عمل المشروع لغاية 31/12/2016", old: "تمتد فترة عمل المشروع لغاية 31/1/2016", effective: day, status: "partial"}
	for _, v := range buildVersions(centreArticle, centre, []*change{c}, 0, false) {
		if v.text == c.old {
			t.Errorf("the replaced phrase became version %d.%d's wording", v.major, v.minor)
		}
	}
}
