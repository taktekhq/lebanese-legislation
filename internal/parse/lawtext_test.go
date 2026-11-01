package parse

import "testing"

func TestCutHeading(t *testing.T) {
	// Code of Civil Procedure 173: the next chapter's heading, with the Centre's typo (انار for انكار).
	body := "يقدم ادعاء التزوير بصورة طارئة امام الغرفة الابتدائية او محكمة الاستئناف . وفي الحالات الاخرى يكون الاختصاص في النظر بادعاء التزوير للغرفة الابتدائية. الفرع الاول - في انار الخط او التوقيع او بصمة الاصبع وفي التطبيق"
	got, ok := cutHeading(body, "الفرع الاول : - في انكار الخط او التوقيع او بصمة الاصبع وفي التطبيق")
	if want := "يقدم ادعاء التزوير بصورة طارئة امام الغرفة الابتدائية او محكمة الاستئناف . وفي الحالات الاخرى يكون الاختصاص في النظر بادعاء التزوير للغرفة الابتدائية."; !ok || got != want {
		t.Errorf("got %q, %v", got, ok)
	}
	// A body that only mentions a chapter, or ends with something else, stays.
	for _, c := range []struct{ body, heading string }{
		{"تطبق احكام هذا القانون على الشركات المذكورة في الفصل الثاني: في قطع الحساب", "الفصل الثالث : - في الموازنة العامة للدولة"},
		{"الفصل الاول - في انكار الخط", "الفصل الاول : - في انكار الخط"},
		// A sentence that ends with a heading's words (Law 258383, article 10).
		{"تقدم الهيئات المشتركة التوصيات وترفعها الى الهيئة التنفيذية عبر مندوبيها في مجلس المندوبين.", "مجلس المندوبين في الهيئة"},
		{"يكون للمساهمين اللبنانيين ما يبلغ او يزيد عن 51% من مجموع الاسهم الحاضرة والممثلة في الجمعية العمومية التأسيسية.", "الجمعيه العموميه التأسيسيه"},
	} {
		if got, ok := cutHeading(c.body, c.heading); ok || got != c.body {
			t.Errorf("%q: cut to %q", c.body, got)
		}
	}
}

// A name heading after a full stop is cut (Law 215626, article 6).
func TestCutNameHeadingAfterStop(t *testing.T) {
	body := "يكون مركز دائرة أوقاف قضائي مرجعيون وحاصبيا في مركز قضاء حاصبيا. المجلس الإداري لأوقاف قضائي مرجعيون وحاصبيا."
	got, ok := cutHeading(body, "المجلس الاداري لاوقاف قضائي مرجعيون وحاصبيا")
	if want := "يكون مركز دائرة أوقاف قضائي مرجعيون وحاصبيا في مركز قضاء حاصبيا."; !ok || got != want {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestEditorNote(t *testing.T) {
	note := Article{Number: "143 *", TextAr: "اضيف هذا الجزء المتعلق في السندات القابلة التحويل الى اسهم بموجب المادة 9 من المرسوم الاشتراعي 54 / 1977"}
	if !editorNote(note) {
		t.Error("COC 143 * is a note")
	}
	if !editorNote(Article{Number: "253 *", TextAr: "اضيف الباب السابع : الشركات المحدودة المسؤولية (مادة 1 الى مادة 35) بموجب المرسوم الاشتراعي 35 /1967"}) {
		t.Error("COC 253 * is a note")
	}
	for _, a := range []Article{
		{Number: "5 *", TextAr: "يعمل بهذا القانون فور نشره في الجريدة الرسمية."},
		{Number: "143", TextAr: note.TextAr},
	} {
		if editorNote(a) {
			t.Errorf("%s is an article", a.Number)
		}
	}
}

// A bare numbered item stays in the Arabic text. Law 329366 article 1 (new
// article 73 of the Code of Criminal Procedure) prints an empty item 4: the
// Centre wraps each item's number in dir="ltr", and "4 -&shy;" has no letters,
// so it used to be taken for French and dropped from the Arabic.
// The markup is archive/lawtext/0329/329366.html, lines 146 to 148.
func TestBareNumberedItemStaysArabic(t *testing.T) {
	item := func(n, rest string) string {
		return `<span dir="ltr"><span style="font-size: 18pt"><span style="font-family: times new roman">` + n +
			`</span></span></span><span style="font-size: 18pt"><span style="font-family: times new roman">` + rest + `</span></span><br />`
	}
	raw := `<html><body><div id="divTreeDetails"><h2>المادة <font>1 </font></h2><div class='text-1'>` +
		item("3", " &shy;- الدفع بعدم قبول الدعوى لسبب يحول دون سماعها أو السير بها قبل البحث في موضوعها.") +
		item("4", " -&shy;") +
		item("5", " &shy;- الدفع بسبق الـملاحقة والحكم.") +
		`<div style="direction: ltr">Article 1 - Texte.</div><div style="direction: ltr">2 -</div>` +
		`</div></div></body></html>`
	lt, err := ParseLawText([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(lt.Articles) != 1 {
		t.Fatalf("%d articles", len(lt.Articles))
	}
	a := lt.Articles[0]
	want := "3 \u00ad- الدفع بعدم قبول الدعوى لسبب يحول دون سماعها أو السير بها قبل البحث في موضوعها.\n\n4 -\u00ad\n\n5 \u00ad- الدفع بسبق الـملاحقة والحكم."
	if a.TextAr != want {
		t.Errorf("Arabic:\n%q\nwant\n%q", a.TextAr, want)
	}
	// A letterless line wholly inside a direction:ltr block is still French.
	if got := deref(a.TextFr); got != "Article 1 - Texte.\n\n2 -" {
		t.Errorf("French: %q", got)
	}
}
