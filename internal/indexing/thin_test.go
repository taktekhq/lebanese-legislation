package indexing

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// testdata/articles.tsv holds real article texts from the database, checked by
// hand: "thin" is a publication, commencement, notification or repeal clause,
// "keep" a short article that says something.
func TestRealArticles(t *testing.T) {
	f, err := os.Open("testdata/articles.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var thin, keep int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		label, text, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			t.Fatalf("bad line %q", sc.Text())
		}
		got := Reason(text, 0) != ""
		switch label {
		case "thin":
			thin++
			if !got {
				t.Errorf("should be thin: %s", text)
			}
		case "keep":
			keep++
			if got {
				t.Errorf("should be kept: %s", text)
			}
		default:
			t.Fatalf("unknown label %q", label)
		}
	}
	if thin < 20 || keep < 20 {
		t.Errorf("too few samples: %d thin, %d keep", thin, keep)
	}
}

func TestEmptyIsThin(t *testing.T) {
	for _, s := range []string{"", "   ", "\n\n", "مادة 3:", "المادة الثانية:"} {
		if got := Reason(s, 0); got != ReasonEmpty {
			t.Errorf("%q: got %q, want empty", s, got)
		}
	}
}

func TestLongTextIsNeverThin(t *testing.T) {
	long := "ينشر هذا المرسوم ويبلغ حيث تدعو الحاجة " + strings.Repeat("ويعمل به فور نشره ", 10)
	if got := Reason(long, 1000); got != "" {
		t.Errorf("a text of %d words is thin (%q)", len(Body(long)), got)
	}
}

func TestLabelIsNotBody(t *testing.T) {
	for in, want := range map[string]int{
		"مادة 2: ينشر هذا المرسوم":         3,
		"المادة الثانية: ينشر هذا المرسوم": 3,
		"المادة 12 مكرر: تلغى كل النصوص":   3,
		"مادة 2: ٣ تعدل":                   2,
		"المواد المتعلقة بالرسوم":          3,
		"ينشر هذا المرسوم ويبلغ حيث تدعو":  6,
	} {
		if got := len(Body(in)); got != want {
			t.Errorf("%q: %d words, want %d (%v)", in, got, want, Body(in))
		}
	}
}

func TestDiacriticsAndLetterVariants(t *testing.T) {
	a := "يُنشَر هذا الـمرسوم ويُبلَّغ حيث تدعو الحاجة"
	b := "ينشر هذا المرسوم ويبلغ حيث تدعو الحاجه"
	if Key(a) != Key(b) {
		t.Errorf("same clause, different keys: %q %q", Key(a), Key(b))
	}
	if Reason(a, 0) != ReasonBoilerplate {
		t.Errorf("diacritics hide the clause: %q", Reason(a, 0))
	}
}

func TestCopiesMakeShortTextBoilerplate(t *testing.T) {
	text := "على الهيئة التأسيسية استكمال اجراءات تأسيس الجمعية خلال مهلة سنة"
	if Reason(text, 3) != "" {
		t.Error("a text seen 3 times is not boilerplate")
	}
	if Reason(text, CopiesThreshold) != ReasonBoilerplate {
		t.Error("a short text copied across many decrees is boilerplate")
	}
}

func TestSubstanceNextToABoilerplatePhraseStays(t *testing.T) {
	for _, s := range []string{
		"مادة 2: يعين رئيس الجمعية بقرار من الوزير وينشر هذا القرار",
		"تلغى المادة 5 من القانون وتحل محلها الاحكام التالية",
		"يعمل بهذا القرار لمدة سنة واحدة اعتبارا من تاريخ نشره",
	} {
		if r := Reason(s, 0); r != "" {
			t.Errorf("%q wrongly thin (%s)", s, r)
		}
	}
}
