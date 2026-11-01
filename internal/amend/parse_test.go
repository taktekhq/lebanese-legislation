package amend

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name, in      string
		action        string
		articles      []string
		part          string
		whole         bool
		kind, number  string
		date, refName string
		wording       []string // prefixes
	}{
		{name: "73", in: "تعدّل الـمادة 73 من القانون رقم 328 تاريخ 2 آب 2001 (اصول الـمحاكمات الجزائية) لتصبح على الشكل الآتي:\n\n«الـمادة 73 الجديدة: الادلاء بالدفوع الشكلية\n\nيحق لكل من الـمدعى عليه أو لوكيله\n\nتسري أحكام هذه الـمادة أمامها».",
			action: Replace, articles: []string{"73"}, kind: KindLaw, number: "328", date: "2001-08-02", refName: "اصول المحاكمات الجزائيه",
			wording: []string{"الادلاء بالدفوع الشكلية\n\nيحق لكل من الـمدعى عليه"}},
		{name: "repeal and substitute", in: "يلغى نص المادة 47 من القانون رقم 479 تاريخ 12/12/2002 ويستبدل بالنص الآتي: «ينشأ صندوق تقاعد ومساعدات اجتماعية للممرضات».",
			action: Replace, articles: []string{"47"}, kind: KindLaw, number: "479", date: "2002-12-12", wording: []string{"ينشأ صندوق تقاعد ومساعدات اجتماعية للممرضات"}},
		{name: "several articles", in: "تعدل المواد 504 و505 و506 لتصبح على الشكل التالي:\nالمادة 504 الجديدة: يعاقب بالأشغال الشاقة.\nالمادة 505 الجديدة: من جامع قاصراً.\nالمادة 506 الجديدة: إذا جامع قاصراً.",
			action: Replace, articles: []string{"504", "505", "506"}, wording: []string{"يعاقب بالأشغال الشاقة.", "من جامع قاصراً.", "إذا جامع قاصراً."}},
		{name: "by name", in: "تعدل المادة 47 من قانون اصول المحاكمات الجزائية لتصبح كالآتي: يتولى الضباط العدليون المهام.",
			action: Replace, articles: []string{"47"}, kind: KindLaw, refName: "اصول المحاكمات الجزائيه", wording: []string{"يتولى الضباط العدليون المهام."}},
		{name: "part, law by date", in: "تعدل الفقرة الخامسة من المادة 71 من قانون 4 كانون الاول سنة 1944 على الصورة التالية: \"رقم القيد في السجل\".",
			action: Replace, articles: []string{"71"}, part: "الفقره الخامسه", kind: KindLaw, date: "1944-12-04", wording: []string{"رقم القيد في السجل"}},
		{name: "ordinal", in: "تعدل المادة الثالثة والسبعون من المرسوم الاشتراعي رقم 112 تاريخ 12 حزيران 1959 بحيث تصبح كما يلي: النص الجديد.",
			action: Replace, articles: []string{"73"}, kind: KindDecreeLaw, number: "112", date: "1959-06-12", wording: []string{"النص الجديد."}},
		{name: "repeal article", in: "تلغى المادة 16 من القانون رقم 132 تاريخ 14/4/1992.",
			action: Repeal, articles: []string{"16"}, kind: KindLaw, number: "132", date: "1992-04-14"},
		{name: "repeal whole text", in: "يلغى اعتبارا من تاريخ العمل بهذا القانون المرسوم الاشتراعي رقم 145 تاريخ 12 حزيران سنة 1959 والنصوص التي تتعارض معه.",
			action: Repeal, whole: true, kind: KindDecreeLaw, number: "145", date: "1959-06-12"},
		{name: "add article", in: "يضاف الى قانون العمل الصادر بتاريخ 23/9/1946، نص المادة التالية: «المادة 50 مكرر: تحدد بموجب مراسيم».",
			action: Insert, articles: []string{"50 مكرر"}, kind: KindLaw, date: "1946-09-23", refName: "العمل", wording: []string{"تحدد بموجب مراسيم"}},
		{name: "add sub-numbered article", in: "يضاف الى قانون العمل الصادر بتاريخ 23/9/1946، نص الـمادة التالية:\n\n«الـمادة 12/1:\n\nفي حال سمحت طبيعة العمل ذلك، يمكن الاتفاق».",
			action: Insert, articles: []string{"12-1"}, kind: KindLaw, date: "1946-09-23", refName: "العمل", wording: []string{"في حال سمحت طبيعة العمل ذلك"}},
		{name: "replace sub-numbered article", in: "تعدل المادة 586 - 3 من قانون العقوبات لتصبح كالآتي: يعاقب بالاعتقال.",
			action: Replace, articles: []string{"586-3"}, kind: KindLaw, refName: "العقوبات", wording: []string{"يعاقب بالاعتقال."}},
		{name: "add to article", in: "تضاف الى المادة 49 من القانون 75 تاريخ 26/12/1967 وتعديلاته (اعادة تنظيم الجامعة اللبنانية) الفقرة التالية: خلافا لأي نص آخر.",
			action: Insert, articles: []string{"49"}, part: "+", kind: KindLaw, number: "75", date: "1967-12-26", refName: "اعاده تنظيم الجامعه اللبنانيه", wording: []string{"خلافا لأي نص آخر."}},
		{name: "number with year", in: "تعدل المادة 7 من القانون رقم 28/2017 لتصبح على الشكل التالي: المادة 7: المستندات الواجب نشرها حكما.",
			action: Replace, articles: []string{"7"}, kind: KindLaw, number: "28/2017", wording: []string{"المستندات الواجب نشرها حكما."}},
		{name: "heading after the colon", in: "تعدل المادة 23 من المرسوم الاشتراعي 35 تاريخ 5/8/1967 وتصبح كما يأتي:\n\nالمادة 23: الجديدة من المرسوم الاشتراعي رقم 35 الصادر بتاريخ 5/8/1967:\n\nفي حال تعددهم يدعى الشركاء.",
			action: Replace, articles: []string{"23"}, kind: KindDecreeLaw, number: "35", date: "1967-08-05", wording: []string{"في حال تعددهم يدعى الشركاء."}},
		{name: "budget law, many articles", in: "تعدل المواد 2، 37 و39 من قانون الرسوم القضائية وتعديلاته الصادر بتاريخ 10/10/1950 لتصبح كما يلي:\n\nالمادة 2 :رسم التسجيل\n\nيحدد رسم التسجيل.\n\nالمادة 37: رسم الصورة\n\nمقدار رسم الصورة هو 10,000 ل.ل.\n\nالمادة 39: رسم الصورة\n\nيستوفى رسم مقداره 10.000 ل.ل.",
			action: Replace, articles: []string{"2", "37", "39"}, kind: KindLaw, date: "1950-10-10", refName: "الرسوم القضائيه",
			wording: []string{"رسم التسجيل\n\nيحدد رسم التسجيل.", "رسم الصورة\n\nمقدار رسم الصورة هو 10,000 ل.ل.", "رسم الصورة\n\nيستوفى رسم مقداره 10.000 ل.ل."}},
		{name: "range", in: "تلغى المواد من 1 الى 4 من المرسوم رقم 8803 تاريخ 4/10/2002.",
			action: Repeal, articles: []string{"1", "2", "3", "4"}, kind: KindDecree, number: "8803", date: "2002-10-04"},
	}
	for _, c := range cases {
		ops := Parse(c.in)
		if len(ops) != 1 {
			t.Errorf("%s: %d ops", c.name, len(ops))
			continue
		}
		op := ops[0]
		date := ""
		if !op.Ref.Date.IsZero() {
			date = op.Ref.Date.Format("2006-01-02")
		}
		got := []any{op.Action, op.Articles, op.Part, op.Whole, op.Ref.Kind, op.Ref.Number, date, op.Ref.Name}
		want := []any{c.action, c.articles, c.part, c.whole, c.kind, c.number, c.date, c.refName}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, want)
		}
		if len(op.Wording) != len(c.wording) {
			t.Errorf("%s: wording %q", c.name, op.Wording)
			continue
		}
		for i, w := range c.wording {
			if !strings.HasPrefix(op.Wording[i], w) {
				t.Errorf("%s: wording %d = %q, want prefix %q", c.name, i, op.Wording[i], w)
			}
		}
	}
}

func TestNone(t *testing.T) {
	for _, in := range []string{
		"تلغى جميع النصوص المخالفة لأحكام هذا القانون او التي لا تتفق مع مضمونه.",
		"ألغيت المواد من 1 الى 30 بموجب المرسوم الاشتراعي رقم 150 تاريخ 16/09/1983.",
		"يعمل بهذا القانون فور نشره في الجريدة الرسمية.",
	} {
		if ops := Parse(in); len(ops) != 0 {
			t.Errorf("%q: %+v", in, ops)
		}
	}
}

func TestOrdinal(t *testing.T) {
	for in, want := range map[string]int{"الاولى": 1, "الحادية عشرة": 11, "الثالثة والسبعون": 73, "الحادية والعشرين": 21,
		"العشرون": 20, "الثامنة عشرة بعد المائة": 118, "الوحيدة": 1} {
		if n, _ := ordinal(strings.Split(FoldString(in), " ")); n != want {
			t.Errorf("%s: %d, want %d", in, n, want)
		}
	}
}

func TestSubNumbers(t *testing.T) {
	for in, want := range map[string]string{
		"12/1: نص":       "12-1",
		"586 - 3 من":     "586-3",
		"586 - (3) من":   "586-3",
		"23 - 1 - يحق":   "23",
		"7 من":           "7",
		"12/9/1946":      "12",
		"5 مكرر 2 من":    "5 مكرر 2",
		"/5/ من القانون": "5",
	} {
		if got, _ := one(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestHeadingWithStrayBracket(t *testing.T) {
	in := "يلغى نص الـمادة 12 من قانون العمل الصادر بتاريخ 23/9/1946، ويستعاض عنه بالنص التالي:\n\n«الـمادة 12 (الجديدة(:\n\n1 ــ عقد العمل الفردي هو اتفاق».\n"
	ops := Parse(in)
	if len(ops) != 1 || len(ops[0].Wording) != 1 || !strings.HasPrefix(ops[0].Wording[0], "1 ــ عقد العمل") {
		t.Fatalf("%+v", ops)
	}
}

// A numbered clause ends where its list goes on, even when the new wording's
// quote is never closed (Law 111/2018 on article 38 of the Rent Law; Law
// 204/2020's article 2, items 6 to 8, on the Penal Code).
func TestClauseEndsAtNextItem(t *testing.T) {
	in := "مادة وحيدة:\n\n1- يعدل نص المادة 38 من القانون رقم 2 تاريخ 28/2/2017 المتعلق بقانون الإيجارات، بحيث يصبح على الشكل التالي:\n\n\"المادة 38 الجديدة:\n\nلحين نفاذ قانون خاص تمدد عقود إيجار الأماكن غير السكنية.\n\nخلال هذه المدة ترتبط بدلات الإيجار.\n\n2- يعمل بهذا القانون فور نشره في الجريدة الرسمية."
	ops := Parse(in)
	if len(ops) != 1 || len(ops[0].Wording) != 1 {
		t.Fatalf("ops = %+v", ops)
	}
	if w := ops[0].Wording[0]; strings.Contains(w, "يعمل بهذا القانون") || !strings.HasSuffix(w, "ترتبط بدلات الإيجار.") {
		t.Errorf("wording = %q", w)
	}

	in = "6- تعدل الـمواد 488 و 489 من قانون العقوبات بحيث تصبح كالآتي:\n\nالـمادة 488: يعاقب أي من الزوجين بالحبس.\n\nالـمادة 489:\n\n-\u00ad لا يجوز ملاحقة فعل الزنا إلا بشكوى.\n\n-\u00ad إذا رضي الـمدعي باستئناف الحياة الـمشتركة تسقط الشكوى.\n\n7 ـ أ ـ\n\nمن أقدم بقصد استيفائه للحقوق الزوجية على ضرب زوجه عوقب.\n\n8 ــ يضاف الى النبذة 6 من الفصل الثاني من قانون العقوبات الـمادة 503 مكرر 1 التالي نصها:\n\nيعاقب بالحبس من ثلاثة أشهر."
	ops = Parse(in)
	if len(ops) == 0 || len(ops[0].Wording) != 2 {
		t.Fatalf("ops = %+v", ops)
	}
	if w := ops[0].Wording[1]; !strings.HasSuffix(w, "تسقط الشكوى.") {
		t.Errorf("489 = %q", w)
	}
}

// A wording with a list of its own keeps all of it.
func TestClauseKeepsInnerList(t *testing.T) {
	in := "1- تعدل المادة 5 من القانون رقم 10 تاريخ 1/1/2000 لتصبح كالآتي:\n1- يعين الوزير لجنة.\n2- تجتمع اللجنة كل شهر.\n3- تصدر قراراتها بالأكثرية."
	ops := Parse(in)
	if len(ops) != 1 || len(ops[0].Wording) != 1 || !strings.HasSuffix(ops[0].Wording[0], "بالأكثرية.") {
		t.Fatalf("ops = %+v", ops)
	}
}

// The new wording of a paragraph 2 may open with its own number 2.
func TestClauseWordingOpensWithNextNumber(t *testing.T) {
	in := "1- تعدل الفقرة الثانية من المادة 5 من القانون رقم 10 تاريخ 1/1/2000 لتصبح كالآتي:\n2- تجتمع اللجنة كل شهر.\nوتصدر قراراتها بالأكثرية."
	ops := Parse(in)
	if len(ops) != 1 || len(ops[0].Wording) != 1 || !strings.HasPrefix(ops[0].Wording[0], "2- تجتمع") {
		t.Fatalf("ops = %+v", ops)
	}
}

// The wording starts after the whole marker word, never inside it
// (amendment 998, law 207767 art. 1, UL lawtext).
func TestMarkerEndsAWord(t *testing.T) {
	in := "تلغى الفقرتان \"ب\" و\"ج\" من المادة الرابعة عشرة من القرار رقم 612/1 تاريخ 26/5/2001, ويستعاض عنهما بالفقرتين \"ب\" و\"ج\" التاليتين:\nب ـ عندما لا يفضي سحب معين إلى وجود تذاكر رابحة."
	ops := Parse(in)
	if len(ops) != 1 || len(ops[0].Wording) != 1 {
		t.Fatalf("ops %+v", ops)
	}
	if w := ops[0].Wording[0]; strings.HasPrefix(w, "ما ") || !strings.HasPrefix(w, "بالفقرتين") {
		t.Errorf("wording starts %q", cut(w, 40))
	}
}

// "بدلا منها" inside the new wording is not the old wording's lead
// (amendment 2680, law 286188 art. 1, UL lawtext).
func TestInsteadEndsAWord(t *testing.T) {
	in := "يعدل البند (5) من المادة السابعة من القرار رقم 175 تاريخ 15/9/2015، بحيث يصبح كما يلي:\n\"البند 5 - ب - طرح قوارير معدنية جديدة صالحة فنيا بدلا منها تكون مطابقة للمواصفات اللبنانية.\""
	ops := Parse(in)
	if len(ops) != 1 || len(ops[0].Wording) != 1 {
		t.Fatalf("ops %+v", ops)
	}
	if ops[0].Old != "" {
		t.Errorf("old %q", ops[0].Old)
	}
	if w := ops[0].Wording[0]; !strings.Contains(w, "بدلا منها تكون مطابقة") {
		t.Errorf("wording %q", w)
	}
	if ops := Parse("تعدل المادة 5 من القانون رقم 12 لتصبح كما يلي: «النص الجديد» بدلا من: «النص القديم»."); len(ops) != 1 || ops[0].Old != "النص القديم" {
		t.Errorf("old lead: %+v", ops)
	}
}
