package provenance

import "testing"

func TestNormalize(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"tatweel and diacritics", "المـــادة الأُولى", "المادة الأولى"},
		{"shadda and tanwin", "يُحَدَّدُ عدداً", "يحدد عددا"},
		{"arabic-indic digits", "المادة ١٢ من القانون ۳۴", "المادة 12 من القانون 34"},
		{"html tags and entities", "<p>نص&nbsp;المادة</p><br/>الثانية &amp; الثالثة", "نص المادة الثانية & الثالثة"},
		{"a less-than in text is not a tag", "x &lt; y و z > w", "x < y و z > w"},
		{"comment", "a<!-- note -->b", "a b"},
		{"nfc", "\u0627\u0654", "\u0623"},
		{"whitespace", "  a \n\t b\u00a0 c  ", "a b c"},
		{"invisible marks", "a\u200cb\u200f\ufeffc", "abc"},
		{"quotation marks", "على «العميل» و\"الصندوق\" و“البنية”", "على العميل والصندوق والبنية"},
		{"an unpaired quote dropped by the build", "يعتبر «قرضا صغيرا»: كل قرض", "يعتبر قرضا صغيرا: كل قرض"},
		{"empty", "", ""},
	} {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("%s: Normalize(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
	// Letters are never folded: a changed letter is a changed word.
	if Normalize("مدرسة") == Normalize("مدرسه") || Normalize("على") == Normalize("علي") {
		t.Error("Normalize folds letters")
	}
}

// A stored text's markup keeps what it holds as words: comment bodies and
// attribute values, quoted or bare, a tag inside a comment too.
func TestNormalizeStored(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"no markup", "نص المادة", "نص المادة"},
		{"bare tags", "<p>نص</p><br/>المادة", "نص المادة"},
		{"comment body kept", "a<!-- hidden words -->b", "a hidden words b"},
		{"attribute values kept", `a<span title="x y" class='z'>b</span>`, "a x y z b"},
		{"bare attribute value", "a<span title=x>b</span>", "a x b"},
		{"a tag inside a comment", `a<!-- <b title="x">y</b> -->c`, "a x y c"},
		{"entities", "x &lt; y", "x < y"},
	} {
		if got := NormalizeStored(c.in); got != c.want {
			t.Errorf("%s: NormalizeStored(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// A dropped quotation mark leaves a word boundary where it stood, though no
// space: «النص» glued to the word before it is still a word of its own.
func TestNormalizeBreaks(t *testing.T) {
	var br []int
	got := normalize("كما يلي«النص» هنا", &br)
	if got != "كما يليالنص هنا" {
		t.Fatalf("normalize = %q", got)
	}
	if want := []int{len("كما يلي"), len("كما يليالنص")}; len(br) != 2 || br[0] != want[0] || br[1] != want[1] {
		t.Errorf("breaks %v, want %v", br, want)
	}
	src := normalizeSource("كما يلي«النص» هنا")
	if !src.spans("النص") || !src.spans("كما يلي") || src.spans("لنص") || src.spans("يليال") {
		t.Error("spans does not keep to the source's words")
	}
}
