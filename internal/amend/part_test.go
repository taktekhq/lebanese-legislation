package amend

import (
	"reflect"
	"slices"
	"testing"
)

// Amending articles that change a part of an article, in the wordings the
// corpus has. Read as a change to the whole article, each made the site
// print one clause as the article (November 2026). The texts are the
// published ones, shortened.
func TestParsePartOfAnArticle(t *testing.T) {
	cases := []struct {
		name, in string
		action   string
		articles []string
		part     string
		edits    bool
		wording  string // when set: the whole wording, and nothing read as replaced
	}{
		{name: "part named after the article's number (Law 323/2023 on Social Security Law article 9)",
			in:     "تعدل الـمادة /9/ البند السادس فقرة (4) (ب) من القانون الـمنفذ بموجب الـمرسوم رقم 13955 الصادر في 26/9/1963 (قانون الضمان الاجتماعي)، وتعديلاته لتصبح كما يلي:\n\n«الـمادة /9/ البند السادس فقرة (4) (ب) (الجديدة):\n\nب -­ في حال توفى الـمتقاعد، أو توفى الـمضمون قبل تقاعده».",
			action: Replace, articles: []string{"9"}, part: "البند السادس فقره (4) (ب)"},
		{name: "part after a dash (Decree 3141/2018)",
			in:     "عدلت المادة الرابعة - الفقرة 6 من المرسوم رقم 8229 تاريخ 2/4/1996 وتعديلاته (النظام الأساسي لصندوق تعاضد افراد الهيئة التعليمية في الجامعة اللبنانية) بحيث اصبح كما يأتي:\n\n\"يجتمع مجلس الادارة اربع مرات في الشهر كحد اقصى بناء على دعوة رسمية من رئيسه او بطلب من ثلاثة اعضاء على الاقل من اعضائه او بطلب من وزير الوصاية.\"",
			action: Replace, articles: []string{"4"}, part: "الفقره 6"},
		{name: "part right after the ordinal (Decision 585/2022)",
			in:     "تعدل الـمادة الثانية ــ الفقرة الرابعة من القرار رقم 469/1 تاريخ 30/5/2016 الـمتعلق بشروط الاجازة الـمسبقة لـمهنة استيراد الاسمدة ومحسنات التربة بحيث تصبح على الشكل التالي:\n\n«صورة عن سند الـملكية وافادة عقارية أو صورة مصدقة حسب الأصول عن عقد إيجار أو عقد استثمار للـمستودع.»",
			action: Replace, articles: []string{"2"}, part: "الفقره الرابعه"},
		{name: "a paragraph added at the end (Law 768/2006 on the sharia courts law, article 17)",
			in:     "عدلت المادة 17 من قانون تنظيم القضاء الشرعي السني والجعفري الصادر بتاريخ 16/7/1962، بإضافة فقرة إلى آخرها وفقا لما يلي:\n\n21 ـ فيما يتعلق بسائر معاملات الوقف الخيري ووقف المساجد والمصليات.",
			action: Insert, articles: []string{"17"}, part: "+"},
		{name: "an item added to it (Decision 513/2009)",
			in:     "تعدل المادة الثانية من القرار 234/1 تاريخ 28/8/2003 ليضاف اليها البند التالي:\n\n- تقوم لجنة من موظفي مديرية الثروة الحيوانية، يشكلها وزير الزراعة، بالكشف على كل مسلخ.",
			action: Insert, articles: []string{"2"}, part: "+"},
		{name: "where a paragraph is added (Law 220/2000 on Social Security Law article 72)",
			in:     "تعدل المادة 72 من قانون الضمان الاجتماعي المنفذ بالمرسوم رقم 13955 تاريخ 26/09/1963 حيث تضاف الفقرة الاتية:\n\n\"يحدد مقدار الاشتراك المتوجب على جمعيات المعوقين ومؤسسات الخدمات بنسبة خمسة عشر بالمئة\".",
			action: Insert, articles: []string{"72"}, part: "+"},
		{name: "by adding the following paragraph (Decision 504/2019)",
			in:     "تعدل المادة الأولى من القرار رقم 490/أ تاريخ 18/6/2019 وذلك بإضافة الفقرة التالية:\n\n«يمكن بصورة إستثنائية، ولمرة واحدة، إصدار طابعي الاوروميد البريديين العاديين بمقاس 4سم (ضرب) 5سم».",
			action: Insert, articles: []string{"1"}, part: "+"},
		{name: "so that the last paragraph becomes (Decree 11553/2003, article 15)",
			in:     "تعدل المادة 15 من المرسوم رقم 2019 تاريخ 10/5/ 1979 بحيث تصبح الفقرة الأخيرة كالتالي:\n\n- يتولى رئاسة مصلحة الاستخدام مستخدم من الملاك الفني من رتبة مساعد باحث على الأقل.",
			action: Replace, articles: []string{"15"}, part: "الفقره الاخيره"},
		{name: "the article's opening (Intermediate Decision 12750/2018)",
			in:     "يلغى مطلع الـمادة الاولى من القرار الاساسي رقم 6504 تاريخ 13/3/1997 ويستبدل بالنص التالي:\n\n\"يطلب من جميع الـمؤسسات الـمالية العاملة في لبنان اعتماد الانموذج 2010 وذلك بغية تنظيم:\"",
			action: Replace, articles: []string{"1"}, part: "مطلع"},
		{name: "items left as they were (Law 533/1996)",
			in:     "تعدل المادة الرابعة من القانون 389 تاريخ 12/01/1995 (انشاء المجلس الاقتصادي الاجتماعي) بحيث تصبح كما يأتي: مادة 4 جديدة: يتألف المجلس من 71 عضوا موزعين على الوجه الاتي:\n\nاولا: بدون تعديل.\n\nثانيا: بدون تعديل.\n\nسابعا: عن اللبنانيين المغتربين في مناطق الانتشار الاغترابي اللبناني - ستة ممثلين.",
			action: Replace, articles: []string{"4"}, edits: true},

		// Still the whole article.
		{name: "a paragraph added, and the article restated as it becomes (Decree 8874/2022)",
			in:     "يُعدل نص المادة الأولى من المرسوم رقم 7564 تاريخ 18/11/1995 لجهة إضافة فقرة «7» إليها بحيث تصبح كالآتي:\n\n«يُعفى «إتحاد الغُرف العربية» في لبنان من الضرائب والرسوم التالية:\n\n1. الضريبة على دخل رؤوس الأموال المنقولة.\n\n7 . الرسوم والعلاوات البلدية كافة.»",
			action: Replace, articles: []string{"1"}},
		{name: "the rest unchanged may be the text's other articles (Decision 1452/2022)",
			in:     "تعدل الـمادة الثانية من القرار رقم 895 تاريخ 31/5/2019 الـمتعلق بحل مجلس بلدية جرمنايا + الرامة ــ محافظة عكار، لتصبح على الشكل التالي:\n\nـ يتولى امين السر العام في محافظة عكار القيام بمهام مجلس بلدية جرمنايا + الرامة بدلا من محافظ عكار حتى انتخاب مجلس بلدي جديد.\n\nوالباقي دون تعديل.",
			action: Replace, articles: []string{"2"},
			wording: "ـ يتولى امين السر العام في محافظة عكار القيام بمهام مجلس بلدية جرمنايا + الرامة بدلا من محافظ عكار حتى انتخاب مجلس بلدي جديد."},
		{name: "an addition in the amended text's description is not this article's (made up)",
			in:     "تعدل المادة 3 من المرسوم رقم 123 تاريخ 1/2/2000 المتعلق بإضافة رسم على الاستيراد وفقا لما يلي: «يفرض رسم قدره خمسة بالمئة على البضائع المستوردة»",
			action: Replace, articles: []string{"3"}, wording: "يفرض رسم قدره خمسة بالمئة على البضائع المستوردة"},
		{name: "an addition an earlier amendment made is not this article's (made up)",
			in:     "تعدل المادة 3 من المرسوم رقم 123 تاريخ 1/2/2000 المعدلة بالقانون رقم 20 تاريخ 1/2/2010 الذي قضى بإضافة فقرة اليها وفقا لما يلي: «يفرض رسم قدره خمسة بالمئة على البضائع المستوردة»",
			action: Replace, articles: []string{"3"}, wording: "يفرض رسم قدره خمسة بالمئة على البضائع المستوردة"},
		{name: "a description, then the addition in a clause of its own (Decision 135/2024 on fertiliser imports, shortened)",
			in:     "تعدّل الـمادة الثانية من القرار رقم 1042/1 تاريخ 7/11/2013 الـمتعلق ب «تنظيم استيراد الاسمدة ومحسنات التربة في لبنان» بحيث يضاف اليها الفقرة التالية:\n\nــ يسمح لـمزارع الفطر باستيراد الاسمدة العضوية بالاضافة الى البيتموس أو محسنات التربة.",
			action: Insert, articles: []string{"2"}, part: "+"},
		{name: "quoted law that says \"without change\" of its own subject lists no items left as they were (made up)",
			in:     "تعدل المادة 7 من القانون رقم 10 تاريخ 1/2/2000 لتصبح كما يلي: «تجدد الرخصة سنويا بالشروط نفسها: دون تعديل في الرسم، وبعد تقديم طلب خطي»",
			action: Replace, articles: []string{"7"}, wording: "تجدد الرخصة سنويا بالشروط نفسها: دون تعديل في الرسم، وبعد تقديم طلب خطي"},
		{name: "the following paragraph names no part (made up, no case in the corpus)",
			in:     "تعدل المادة 5 من القانون رقم 28/2017 لتصبح الفقرة التالية: تنشر الادارة قراراتها.",
			action: Replace, articles: []string{"5"}},
	}
	for _, c := range cases {
		ops := Parse(c.in)
		if len(ops) != 1 {
			t.Errorf("%s: %d ops", c.name, len(ops))
			continue
		}
		op := ops[0]
		got := []any{op.Action, op.Articles, op.Part, slices.Contains(op.Flags, "edits")}
		want := []any{c.action, c.articles, c.part, c.edits}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, want)
		}
		if len(op.Wording) != 1 || op.Wording[0] == "" {
			t.Errorf("%s: wording %q", c.name, op.Wording)
		}
		if c.wording != "" && (len(op.Wording) != 1 || op.Wording[0] != c.wording || op.Old != "") {
			t.Errorf("%s:\n wording %q\n old     %q\n want the wording %q and no old one", c.name, op.Wording, op.Old, c.wording)
		}
	}
}

// "بدلا من" quotes the wording replaced when it follows the new wording; in
// a sentence of the new wording it is that sentence's own.
func TestParseInsteadOf(t *testing.T) {
	cases := []struct{ name, in, wording, old string }{
		{"the old wording quoted after the new one (Decree 3416/2016)",
			"عدلت المادة الثانية من المرسوم رقم 2336 تاريخ 24/11/2015 لتصبح كالتالي:\n\n«تمتد فترة عمل المشروع لغاية 31/12/2016».\n\nبدلا من: «تمتد فترة عمل المشروع لغاية 31/1/2016».\n\nوالباقي دون تعديل.",
			"تمتد فترة عمل المشروع لغاية 31/12/2016", "تمتد فترة عمل المشروع لغاية 31/1/2016».\n\nوالباقي دون تعديل."},
		{"on its own line, with no quote closed before it (Decree 3329/2018)",
			"تُعدل المادة الأولى من المرسوم 1949 تاريخ 5/12/2017 لتصبح كالتالي:\n\n\"تمتد فترة عمل المشروع لغاية 30/6/2019\n\nبدلاً من:\n\n\"تمتد فترة عمل المشروع لغاية 30/6/2018",
			"تمتد فترة عمل المشروع لغاية 30/6/2019", "تمتد فترة عمل المشروع لغاية 30/6/2018"},
		{"inside the new sentence (Decision 882/2023)",
			"تعدل الـمادة الأولى من القرار رقم 1451 تاريخ 5/12/2022 الـمتعلقة بحل مجلس بلدية مشتى حمود ــ محافظة عكار، ليصبح على الشكل التالي:\n\nــ يتولى محافظ عكار القيام بمهام مجلس بلدية مشتى حمود بدلا من امين سر عام محافظة عكار حتى انتخاب مجلس بلدي فيها.\n\nوالباقي دون تعديل.",
			"ــ يتولى محافظ عكار القيام بمهام مجلس بلدية مشتى حمود بدلا من امين سر عام محافظة عكار حتى انتخاب مجلس بلدي فيها.", ""},
		{"inside the new sentence, far from its end (Law 89/1991, article 42)",
			"يعدل نص المادة الرابعة من المرسوم الاشتراعي رقم 46 تاريخ 24/6/1983 (نظام الشركات المحصور نشاطها خارج لبنان اوف شور) ويصبح كما يلي:\n\nتعفى الشركة من ضريبة الدخل على الارباح وتخضع بدلا من ذلك لضريبة سنوية مقطوعة قدرها مليون ليرة, تسدد مباشرة الى الدائرة المالية المختصة بضريبة الدخل. وتطبق هذه الضريبة على الشركة ابتداء من اول سنة مالية مهما كانت مدتها.",
			"تعفى الشركة من ضريبة الدخل على الارباح وتخضع بدلا من ذلك لضريبة سنوية مقطوعة قدرها مليون ليرة, تسدد مباشرة الى الدائرة المالية المختصة بضريبة الدخل. وتطبق هذه الضريبة على الشركة ابتداء من اول سنة مالية مهما كانت مدتها.", ""},
		{"told in the sentence, with no colon before the wording: still a phrase and the one it replaces (Decree 1562/2017)",
			"عدلت المادة الأولى من المرسوم رقم 3868 تاريخ 21/7/2016 لجهة استبدال اسم الجهة الواهبة لتصبح السفارة البلجيكية بدلاً من السيد رياض مطانيوس طوق.\n\nوالباقي دون تعديل.",
			"السفارة البلجيكية", "السيد رياض مطانيوس طوق.\n\nوالباقي دون تعديل."},
	}
	for _, c := range cases {
		ops := Parse(c.in)
		if len(ops) != 1 || len(ops[0].Wording) != 1 {
			t.Errorf("%s: %d ops", c.name, len(ops))
			continue
		}
		if got := ops[0]; got.Wording[0] != c.wording || got.Old != c.old {
			t.Errorf("%s:\n wording %q\n old     %q\n want    %q\n and old %q", c.name, got.Wording[0], got.Old, c.wording, c.old)
		}
	}
}
