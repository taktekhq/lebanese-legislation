package site

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

// annexLaw is Law 243/2012 with two articles and part of its annexed tables,
// as attachGazette builds them from gazette_annex.
func annexLaw() *Law {
	on := time.Date(2012, 10, 25, 0, 0, 0, 0, time.UTC)
	a374 := &Article{Position: 1, Number: "374", TextAr: "تُصنّف المخالفات التي ينظم فيها محضر، في الفئات التالية:"}
	a375 := &Article{Position: 2, Number: "375", TextAr: "إن العقوبات والغرامات والمخالفات، مبينة في هذا القانون وفي جداول المخالفات الملحقة به."}
	l := &Law{LawRow: LawRow{ID: 246455, Kind: "قانون", Number: ptr("243"), IssuedOn: day(2012, 10, 22), Title: "قانون السير الجديد",
		Status: "in_force", Articles: 2}, List: []*Article{a374, a375}, Items: []Item{{Article: a374}, {Article: a375}}}
	pdf := "https://saderlex.com/gazette/pdf/170879"
	l.Annex = &Annex{Issue: "45", Date: &on, PDF: pdf, Decided: 1, Traffic: true, pages: []int{4970, 4971, 4972, 5001},
		Tables: []AnnexTable{
			{Category: "3", Title: "جدول مخالفات السير فئة ثالثة", FineMin: 350000, FineMax: 450000, First: 4970, Last: 4972, PDF: pdf + "#page=82",
				Rows: []AnnexRow{
					{Anchor: "annex-3-22", Serial: 22, Article: "١٧", ArticleNum: "17", Violation: "استعمال أي جهاز من اجهزة الاتصال اثناء القيادة.", Points: "٣"},
					{Anchor: "annex-3-34", Serial: 34, Article: "۲۳", ArticleNum: "23", Violation: "عدم تخفيف السرعة لدى التلاقي مع حيوانات جر أو حمل او ركوب او مواشي او لدى تجاوزها",
						DecidedBy: "gemini-4-argon-high", Others: []AnnexReading{{Field: "points", Value: "۳", Source: "reader"}}},
					{Anchor: "annex-3-77", Serial: 77, Article: "١٣٣", ArticleNum: "133", Violation: "السير بمركبة يزيد وزنها الاجمالي عن ١٠% وحتى ٢٠% عن الوزن الاجمالي المحدد لها في رخصة السير",
						Points: "٣", Notes: "احتجاز حتى ازالة المخالفة", ReplacedBy: &AnnexLink{LawID: 236991, Article: "1", Cite: "القانون رقم 278"}},
				}},
			{Category: "p", Title: "جدول مخالفات المشاة", FineMin: 50000, FineMax: 100000, First: 5001, Last: 5001, PDF: pdf + "#page=113",
				Rows: []AnnexRow{{Anchor: "annex-p-1", Serial: 1, Article: "٤٦", ArticleNum: "46", Violation: "عبور الطريق خارج الممرات المخصصة للمشاة"}}},
		}}
	return l
}

func renderAs(t *testing.T, s *Server, lang, path, page string, p Page) string {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r = r.WithContext(context.WithValue(r.Context(), langCtxKey, lang))
	body, err := s.execute(r, page, p)
	if err != nil {
		t.Fatalf("%s %s: %v", lang, path, err)
	}
	return body
}

// Law 243's annexed tables show after its articles, in both languages, with
// the notice, each category's table and caption, every row's anchor, the
// rows a model decided and the rows Law 278/2014 replaced marked (Nizar, 5
// Nov 2026: "just put them live"). An older database has no annex: nothing.
func TestGazetteAnnexSection(t *testing.T) {
	s, err := newServer(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "ar"} {
		l := annexLaw()
		body := renderAs(t, s, lang, "/law/246455/", "law", Page{Path: lawPath(l.ID), Data: l})
		want := map[string][]string{
			"en": {"Annexed tables, read from the Official Gazette's pages", "issue 45 of 25/10/2012, pages 4970-4972, 5001",
				"those 1 rows are marked", "<b>Category 3</b>", "fine of 350,000 to 450,000 LBP", "Gazette pages 4970-4972",
				"<b>Pedestrians</b>", "Reading decided by Gemini from the page.", "Other reading (points, machine reading):",
				"Article 42 of Law 324 of 2024", "This is the 2012 wording."},
			"ar": {"الجداول الملحقة بالقانون، كما قُرئت من صفحات الجريدة الرسمية", "العدد 45 تاريخ 25/10/2012، الصفحات 4970-4972، 5001",
				"<b>الفئة الثالثة</b>", "غرامة من 350,000 إلى 450,000 ليرة لبنانية", "<b>المشاة</b>",
				"قرّر Gemini هذه القراءة من صورة الصفحة.", "قراءة أخرى (النقاط، قراءة آلية):", "وهذا نصه عام 2012."},
		}[lang]
		want = append(want, `<section class="wrap sec annex" id="annex">`, `id="annex-3" lang="ar" dir="rtl"`,
			`<tr id="annex-3-22"><th scope="row" class="annex__n">22</th>`, `<tr id="annex-3-34" class="annex__decided">`,
			`<tr id="annex-3-77" class=" annex__replaced">`, `<tr id="annex-p-1">`, `href="https://saderlex.com/gazette/pdf/170879"`,
			`href="https://saderlex.com/gazette/pdf/170879#page=113"`, `<div class="tablewrap"><table class="ref annex__table"`,
			"استعمال أي جهاز من اجهزة الاتصال اثناء القيادة.", "<bdi lang=\"ar\">۳</bdi>")
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s law page lacks %q", lang, w)
			}
		}
		prefix := map[string]string{"en": "/en", "ar": ""}[lang]
		for _, link := range []string{prefix + "/law/246455/article/17/", prefix + "/law/246455/article/374/", prefix + "/law/330076/article/42/", prefix + "/law/236991/article/1/"} {
			if !strings.Contains(body, `href="`+link+`"`) {
				t.Errorf("%s law page does not link %s", lang, link)
			}
		}
		if i, j := strings.Index(body, `id="a-375"`), strings.Index(body, `id="annex"`); i < 0 || j < i {
			t.Errorf("%s: the annex is not after the articles (%d, %d)", lang, i, j)
		}
		if strings.Contains(body, "—") {
			t.Errorf("%s law page has an em dash", lang)
		}
		l.Annex = nil // an older database
		if body := renderAs(t, s, lang, "/law/246455/", "law", Page{Path: lawPath(l.ID), Data: l}); strings.Contains(body, `id="annex"`) || strings.Contains(body, "annex__") {
			t.Errorf("%s: a law without an annex shows one", lang)
		}
		// A long text's first part links the tables on its last part (serveLaw).
		l.Part, l.AnnexPart = 1, "/law/246455/part/2/"
		l.Parts = []LawPart{{N: 1, First: "374", Last: "374", Path: "/law/246455/"}, {N: 2, First: "375", Last: "375", Path: "/law/246455/part/2/"}}
		if body := renderAs(t, s, lang, "/law/246455/", "law", Page{Path: lawPath(l.ID), Data: l}); !strings.Contains(body, `href="`+prefix+`/law/246455/part/2/#annex"`) {
			t.Errorf("%s: part 1 does not link the annex on part 2", lang)
		}
	}
}

// The Social Security Law's Articles 49 to 54 carry their 1963 wording,
// folded under the Lebanese University's text, which stays the text (Nizar's
// rule): on the law page and on the article's own page, in both languages.
func TestArticleOriginalWording(t *testing.T) {
	s, err := newServer(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	on := time.Date(1963, 9, 30, 0, 0, 0, 0, time.UTC)
	ul := "مادة 52-1: خلفاء الـمضمون:"
	first := "لا يحق للاجير المضمون الزاميا أو اختياريا الا تعويض مخفض في الحالات التالية:\n\n- ٥٠ ٪ من التعويض المذكور اذا اشترك خمس سنوات على الاكثر ."
	law := func() (*Law, *Article) {
		a := &Article{Position: 1, Number: "52", TextAr: ul, Original: &Original{Anchor: "original-52", Text: first, Issue: "78", Date: &on,
			Pages: "14-17", Supplement: true, PDF: "https://saderlex.com/gazette/pdf/77024"}}
		return &Law{LawRow: LawRow{ID: 244971, Kind: "قانون منفذ بمرسوم", Number: ptr("13955"), IssuedOn: day(1963, 9, 26),
			Title: "قانون الضمان الاجتماعي", Status: "in_force", Articles: 1}, List: []*Article{a}, Items: []Item{{Article: a}}}, a
	}
	for _, lang := range []string{"en", "ar"} {
		want := map[string][]string{
			"en": {"Wording as first published in 1963, read from the Official Gazette's pages", "Official Gazette issue 78 of 30/09/1963 (supplement), pages 14-17.",
				"This copy is unofficial: check the original PDF."},
			"ar": {"النص كما نُشر عام 1963، مقروءاً من صفحات الجريدة الرسمية", "الجريدة الرسمية، ملحق العدد 78 تاريخ 30/09/1963، الصفحات 14-17.",
				"هذه نسخة غير رسمية: راجع ملف PDF الأصلي."},
		}[lang]
		want = append(want, `<details class="art__original">`, `<div class="art__original-body" id="original-52">`,
			`href="https://saderlex.com/gazette/pdf/77024"`, "<p>- ٥٠ ٪ من التعويض المذكور اذا اشترك خمس سنوات على الاكثر .</p>")
		l, _ := law()
		lawBody := renderAs(t, s, lang, "/law/244971/", "law", Page{Path: lawPath(l.ID), Data: l})
		r := httptest.NewRequest("GET", "/law/244971/article/52/", nil)
		r = r.WithContext(context.WithValue(r.Context(), langCtxKey, lang))
		l, _ = law()
		p := s.articlePage(r, l, 0)
		p.Data.(map[string]any)["Topics"] = []TopicRef(nil)
		artBody := renderAs(t, s, lang, "/law/244971/article/52/", "article", p)
		for name, body := range map[string]string{"law page": lawBody, "article page": artBody} {
			for _, w := range want {
				if !strings.Contains(body, w) {
					t.Errorf("%s %s lacks %q", lang, name, w)
				}
			}
			if i, j := strings.Index(body, ul), strings.Index(body, `class="art__original"`); i < 0 || j < i {
				t.Errorf("%s %s: the University's text is not shown first (%d, %d)", lang, name, i, j)
			}
		}
		l, a := law()
		a.Original = nil // an older database
		if body := renderAs(t, s, lang, "/law/244971/", "law", Page{Path: lawPath(l.ID), Data: l}); strings.Contains(body, "art__original") {
			t.Errorf("%s: an article without a first wording shows one", lang)
		}
	}
}

func TestAnnexPagesAndReadings(t *testing.T) {
	a := &Annex{pages: []int{4970, 4971, 4972, 4995, 5001}}
	if got := a.PagesIn("en"); got != "4970-4972, 4995, 5001" {
		t.Errorf("PagesIn en: %q", got)
	}
	if got := a.PagesIn("ar"); got != "4970-4972، 4995، 5001" {
		t.Errorf("PagesIn ar: %q", got)
	}
	raw := []byte(`{"violation":{"candidates":[{"source":"Gazette reader gemini-3.8-flash-high","value":"عدم حمل رخصة السوق"},
		{"source":"Gazette reader gemini-4-argon-medium","value":"عدم نقل رخصة السوق"},{"source":"ISF consolidated copy","value":"عدم نقل  رخصة السوق"}],
		"chosen":"Gazette reader gemini-4-argon-medium"}}`)
	got := otherReadings(raw, map[string]string{"violation": "عدم نقل رخصة السوق"}, false)
	if len(got) != 1 || got[0].Value != "عدم حمل رخصة السوق" || got[0].Source != "reader" || got[0].Field != "violation" {
		t.Errorf("other readings: %+v", got)
	}
	isf := []byte(`{"violation":{"candidates":[{"source":"Gazette readers (both agree)","value":"أ"},{"source":"ISF consolidated copy","value":"ب"}]}}`)
	if got := otherReadings(isf, map[string]string{"violation": "أ"}, false); len(got) != 1 || got[0].Source != "isf" {
		t.Errorf("ISF reading: %+v", got)
	}
	if got := otherReadings(isf, map[string]string{"violation": "أ"}, true); len(got) != 0 {
		t.Errorf("a replaced row lists the ISF's 2014 wording: %+v", got)
	}
	if plainNumber("١٣٣") != "133" || plainNumber("٣٤ و٦٨") != "" {
		t.Error("plainNumber")
	}
}

// A topic's cite of a Gazette reading links where the reading is shown and
// is named for what it is, not as an article (citeTarget).
func TestCiteTargetReadings(t *testing.T) {
	s, err := newServer(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		cite                   topics.Cite
		path, labelEn, labelAr string
	}{
		{topics.Cite{Law: 246455, Article: "annex-3-22"}, "/law/246455/#annex-3-22", "Annexed table, category 3, row 22", "الجدول الملحق، الفئة 3، الرقم 22"},
		{topics.Cite{Law: 246455, Article: "annex-p-4"}, "/law/246455/#annex-p-4", "Annexed pedestrians' table, row 4", "جدول مخالفات المشاة الملحق، الرقم 4"},
		{topics.Cite{Law: 244971, Article: "original-52"}, "/law/244971/article/52/#original-52", "Article 52 as first published", "النص الأصلي للمادة 52"},
		{topics.Cite{Law: 246455, Article: "374"}, "/law/246455/article/374/", "", ""},
	}
	for _, c := range cases {
		for lang, want := range map[string]string{"en": c.labelEn, "ar": c.labelAr} {
			r := httptest.NewRequest("GET", "/topic/x/", nil)
			r = r.WithContext(context.WithValue(r.Context(), langCtxKey, lang))
			path, label := s.citeTarget(r.Context(), r, &c.cite)
			if path != c.path || label != want {
				t.Errorf("%s %s: %q %q, want %q %q", lang, c.cite.Article, path, label, c.path, want)
			}
		}
	}
	if got := readingNameAr("annex-5-1"); got != "الجدول الملحق، الفئة 5، الرقم 1" {
		t.Errorf("readingNameAr: %q", got)
	}
}
