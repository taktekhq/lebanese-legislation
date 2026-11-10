package build

import (
	"testing"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/amend"
	"github.com/taktekhq/lebanese-legislation/internal/modelout"
)

// printed is the wording the site prints for an article with these
// versions (the latest one that has a wording), and where it was read.
func printed(vs []version) (string, string) {
	for i := len(vs) - 1; i >= 0; i-- {
		if vs[i].text != "" {
			return vs[i].text, vs[i].source
		}
	}
	return "", ""
}

// layout runs an article's changes the way versions does: place, then
// buildVersions.
func layout(centre string, cs ...*change) []version {
	cs, at, unknown := place(centre, cs, cs[0].law, seenTimes{}, modelout.Judgements{})
	if len(cs) == 0 {
		return nil
	}
	return buildVersions(centre, textSource{"ul", "lawtext/1"}, cs, at, unknown)
}

// Decree 12045/2014, article 8 as the Centre has it, and Decree 2894/2018,
// which replaces it with one line and quotes all of the old article.
const (
	grantArticle = "مادة 8: تحدد فترة عمل مشروع الدعم التقني لمكافحة التلوث البيئي في لبنان موضوع الهبة بثلاثة سنوات بدءً من تاريخ توقيع مذكرة التفاهم بين السفارة الايطالية في لبنان ووزارة البيئة اي بتاريخ 13/02/2014، قابلة للتجديد بعد موافقة الحكومة الايطالية ممثلة بالسفارة الايطالية والحكومة اللبنانية ممثلة بوزارة البيئة."
	grantOld     = "تحدد فترة عمل الدعم التقني لمكافحة التلوث البيئي في لبنان موضوع الهبة بثلاثة سنوات بدءً من تاريخ توقيع مذكرة التفاهم بين السفارة الايطالية في لبنان ووزارة البيئة اي بتاريخ 13/2/2014، قابلة للتجديد بعد موافقة الحكومة الايطالية ممثلة بالسفارة الايطالية والحكومة اللبنانية ممثلة بوزارة البيئة"
	grantNew     = "تمتد فترة عمل الدعم التقني لمشروع مكافحة التلوث البيئي في لبنان لغاية 31/1/2021"
)

// The guards as versions runs them (place): a wording that is a part of
// the article is never printed as the article, and three kinds of short
// wording still are.
func TestPlaceGuards(t *testing.T) {
	day := time.Date(2018, 5, 10, 0, 0, 0, 0, time.UTC)
	mk := func(text string) *change {
		return &change{id: 7, byLaw: 2, byArticle: "1", seq: 1, action: amend.Replace, law: 1, article: "8",
			text: text, effective: day, status: "applied", method: "pattern"}
	}

	// Under half the article, nothing else known: a note on the Centre's text.
	c := mk("21 ـ فيما يتعلق بسائر معاملات الوقف الخيري")
	c.article = "17"
	vs := layout("يدخل في اختصاص المحاكم الشرعية الدعاوى والمعاملات المتعلقة بالأمور الآتية:\n\n1 - الخطبة وهدايا الخطبة.\n\n2 - الزواج العقد.\n\n3 - المهر والجهاز.\n\n20 - الوقف الذري.", c)
	if got, src := printed(vs); src != "centre" || c.status != "partial" {
		t.Errorf("short: status %s, printed %q from %s; want partial and the Centre's text", c.status, got, src)
	}
	if last := vs[len(vs)-1]; last.amendment != 7 || last.text != "" {
		t.Errorf("short: the change's version has wording %q (amendment %d), want a pending one with none", last.text, last.amendment)
	}

	// Inside the Centre's longer text: the Centre's text, which carries it.
	c = mk(addedClause)
	if got, src := printed(layout(centreArticle, c)); got != centreArticle || src != "centre" || !c.inside || c.status != "applied" {
		t.Errorf("inside: status %s, inside %v, printed from %s; want applied, inside, the Centre's text", c.status, c.inside, src)
	}

	// Short, but the amending article quotes the whole old article: the new
	// wording is the whole article.
	c = mk(grantNew)
	c.old = grantOld
	if got, src := printed(layout(grantArticle, c)); got != grantNew || src != "amendment" || c.status != "applied" {
		t.Errorf("old article quoted in full: status %s, printed %q from %s; want the amending wording", c.status, got, src)
	}

	// The same short wording quoting only a phrase of the article: a part.
	c = mk(grantNew)
	c.old = "تحدد فترة عمل الدعم التقني"
	if _, src := printed(layout(grantArticle, c)); src != "centre" || c.status != "partial" {
		t.Errorf("a phrase quoted: status %s, printed from %s; want partial and the Centre's text", c.status, src)
	}

	// Short, and a person confirmed it (db/amendments.tsv): printed.
	c = mk(grantNew)
	c.method = "editorial"
	if got, src := printed(layout(grantArticle, c)); got != grantNew || src != "amendment" || c.status != "applied" {
		t.Errorf("confirmed by a person: status %s, printed %q from %s; want the amending wording", c.status, got, src)
	}

	// Inside the Centre's text, and a person confirmed it: printed too.
	c = mk(addedClause)
	c.method = "editorial"
	if got, src := printed(layout(centreArticle, c)); got != addedClause || src != "amendment" || c.inside {
		t.Errorf("inside, confirmed by a person: inside %v, printed %q from %s; want the amending wording", c.inside, got, src)
	}
}

// An apply row is a person's word that the wording is the whole article:
// it passes the guards, and it undoes a part or an addition the pattern
// pass read into the amending article.
func TestApplyRowPrintsTheWording(t *testing.T) {
	day := time.Date(2018, 5, 10, 0, 0, 0, 0, time.UTC)
	row := []amend.Decision{{ByLaw: 2, ByArticle: "1", Law: 1, Article: "8", Decision: amend.DecideApply}}
	none := func(int, string) (string, bool) { return "", false }
	note := func(string, ...any) {}
	cases := []struct {
		name   string
		change change
	}{
		{"a short wording", change{action: amend.Replace, status: "applied"}},
		{"read as a change to a part", change{action: amend.Replace, part: "الفقره الاخيره", status: "partial"}},
		{"read as an addition", change{action: amend.Insert, part: "+", status: "partial"}},
	}
	for _, tc := range cases {
		c := tc.change
		c.id, c.byLaw, c.byArticle, c.seq, c.law, c.article = 7, 2, "1", 1, 1, "8"
		c.text, c.effective, c.method = grantNew, day, "pattern"
		out := applyDecisions([]*change{&c}, row, nil, none, note)
		if len(out) != 1 || c.status != "applied" || c.method != "editorial" || c.part != "" || c.action != amend.Replace {
			t.Errorf("%s: %d changes; status %s, method %s, part %q, action %s", tc.name, len(out), c.status, c.method, c.part, c.action)
			continue
		}
		if got, src := printed(layout(grantArticle, out...)); got != grantNew || src != "amendment" {
			t.Errorf("%s: printed %q from %s, want the amending wording", tc.name, got, src)
		}
	}

	// Without the row the same three print the Centre's text.
	for _, tc := range cases {
		c := tc.change
		c.id, c.byLaw, c.byArticle, c.seq, c.law, c.article = 7, 2, "1", 1, 1, "8"
		c.text, c.effective, c.method = grantNew, day, "pattern"
		if _, src := printed(layout(grantArticle, &c)); src != "centre" {
			t.Errorf("%s, no row: printed from %s, want the Centre's text", tc.name, src)
		}
	}
}

func TestRestated(t *testing.T) {
	c := &change{text: grantNew, old: grantOld}
	if !restated(grantArticle, c) {
		t.Error("the old article quoted in full: restated")
	}
	c.old = "تحدد فترة عمل الدعم التقني"
	if restated(grantArticle, c) {
		t.Error("a phrase of the article quoted: not restated")
	}
	c.old = ""
	if restated(grantArticle, c) {
		t.Error("nothing quoted: not restated")
	}
	c.old = grantOld
	if restated("يحدد بدل الأتعاب بمبلغ 40,000,000 ليرة لبنانية شهريا لكل محام معتمد لدى هيئة القضايا في وزارة العدل، ويعتبر ثلث هذا المبلغ بدل انتقال مقطوع.", c) {
		t.Error("the wording quoted is not the Centre's: not restated")
	}
}
