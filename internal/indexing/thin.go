// Package indexing decides which pages search engines are asked to index:
// thin article pages (a publication clause, a repeal-only clause, no text) and
// placeholder documents. The build runs these rules once and stores the
// result (table noindex_page), so the site and the sitemaps read one answer.
package indexing

import (
	"strings"
	"unicode"
)

// MaxThinWords is the length under which a boilerplate article is thin.
const MaxThinWords = 25

// CopiesThreshold is how many articles must share the same short text for it
// to count as boilerplate even when no phrase list recognises it.
const CopiesThreshold = 20

// Thin reasons.
const (
	ReasonEmpty       = "empty"
	ReasonBoilerplate = "boilerplate"
)

// Normalize reduces Arabic text to a form for matching: no diacritics or
// tatweel, one letter for each family of alef, yeh and heh, punctuation as
// spaces. It is for comparing, never for display.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == 0x640 || (r >= 0x64B && r <= 0x652) || r == 0x670 || r == 0x200f || r == 0x200e:
			continue
		case r == 'أ' || r == 'إ' || r == 'آ' || r == 'ٱ':
			b.WriteRune('ا')
		case r == 'ى':
			b.WriteRune('ي')
		case r == 'ة':
			b.WriteRune('ه')
		case r >= '٠' && r <= '٩':
			b.WriteRune('0' + (r - '٠'))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

var ordinals = map[string]bool{"الاولي": true, "الثانيه": true, "الثالثه": true, "الرابعه": true, "الخامسه": true,
	"السادسه": true, "السابعه": true, "الثامنه": true, "التاسعه": true, "العاشره": true, "الحاديه": true}

// Body is the normalised words of an article's text without its own
// "article N" label (مادة 2: ... or المادة الثانية: ...).
func Body(text string) []string {
	t := strings.Fields(Normalize(text))
	if len(t) > 0 && (t[0] == "ماده" || t[0] == "الماده") {
		t = t[1:]
		if len(t) > 0 && (isNumber(t[0]) || ordinals[t[0]]) {
			t = t[1:]
			if len(t) > 0 && t[0] == "مكرر" {
				t = t[1:]
			}
		}
	}
	return t
}

func isNumber(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// strip takes the common one-letter prefixes (and, with, to) and the article
// "ال" off a word, so ويبلغ matches يبلغ and للاحكام matches احكام.
func strip(w string) string {
	if len(w) > 6 && w[:2] == "و" { // و is two bytes in UTF-8
		w = w[2:]
	}
	for _, p := range []string{"بال", "لل", "ال", "ب", "ل"} {
		if strings.HasPrefix(w, p) && len([]rune(w)) > len([]rune(p))+2 {
			return w[len(p):]
		}
	}
	return w
}

// Words that make up a publication, commencement or notification clause.
var publishWords = set(`ينشر تنشر نشر يبلغ تبلغ يعمل به بها بهذا بهذه هذا هذه المرسوم القرار القانون النظام العلم الخبر
	فور نشره نشرها صدوره صدورها في الجريده الرسميه حيث تدعو الحاجه من يلزم الي صاحب العلاقه
	علي نفقه المؤسسين موقع وزاره الماليه الالكتروني وعلي الاشتراعي التطبيقي الاتفاقيه اعتبارا
	من تاريخ تاريخ ويبلغ وينشر ويعمل المراسيم اللوائح بعد ذلك مباشره التعميم الاعلان اصحاب العلاقه كل
	الاشتراكي القرارات المعنيين المعني تعليقه لوحه الاعلانات الاقتصاد الوطني وسيله اعلاميه اخري اول الشهر الذي يلي
	ان امين سر الدوله مكلف مكلفون تنفيذ كلا منهم فيما يخصه وزير وزراء الوزراء`)

// Words of a repeal-only clause ("all provisions contrary to this law are repealed").
var repealWords = set(`يلغي تلغي الغيت الغي ملغاه ملغي ملغيه كل جميع نص نصوص احكام حكم مخالف مخالفه المخالفه المخالفة
	لهذا هذا هذه القانون المرسوم القرار النظام ما يتعارض مع يتعارض وكل وجميع ولاسيما لاسيما خاصه
	بموجب رقم تاريخ سنه القانون الصادر الصادره السابق السابقه او غير المتفقه مضمون مضمونه`)

var repealMarkers = set(`يلغي تلغي الغيت الغي ملغاه ملغي ملغيه`)
var publishMarkers = set(`ينشر تنشر نشر يبلغ تبلغ يعمل مكلف مكلفون`)

var (
	clauseWords   = merge(publishWords, repealWords)
	clauseMarkers = merge(publishMarkers, repealMarkers)
)

func set(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[strip(Normalize(w))] = true
		m[Normalize(w)] = true
	}
	return m
}

func merge(a, b map[string]bool) map[string]bool {
	m := map[string]bool{}
	for k := range a {
		m[k] = true
	}
	for k := range b {
		m[k] = true
	}
	return m
}

// onlyClause reports whether every word is in vocab or a number, with at
// least one marker among them.
func onlyClause(words []string, vocab, markers map[string]bool) bool {
	marked := false
	for _, w := range words {
		s := strip(w)
		if markers[w] || markers[s] {
			marked = true
			continue
		}
		if vocab[w] || vocab[s] || isNumber(w) {
			continue
		}
		return false
	}
	return marked
}

// Reason says why an article page is thin, or "" when it is not.
// copies is how many articles in the corpus have exactly the same body.
func Reason(text string, copies int) string {
	words := Body(text)
	if len(words) == 0 {
		return ReasonEmpty
	}
	if len(words) >= MaxThinWords {
		return ""
	}
	// a clause may publish, bring into force and repeal all at once
	if onlyClause(words, clauseWords, clauseMarkers) {
		return ReasonBoilerplate
	}
	if copies >= CopiesThreshold {
		return ReasonBoilerplate
	}
	return ""
}

// Key is the body the corpus counts copies of.
func Key(text string) string { return strings.Join(Body(text), " ") }
