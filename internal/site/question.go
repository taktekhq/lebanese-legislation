package site

import (
	"strings"
	"unicode"
)

// IsQuestion says whether a search reads as a question someone wants answered
// ("هل يمكن للمدعي ... بالتعويض؟", "how many days of annual leave") rather
// than a lookup of a text ("قانون العمل", "المادة 248 موجبات وعقود",
// "decree 2552/1992"). Only questions get a written AI answer; lookups go
// straight to the matching texts. No model is called to decide.
func IsQuestion(q string) bool {
	q = strings.TrimSpace(arDigits.Replace(q))
	if q == "" || WantsDownload(q) {
		return false
	}
	if _, _, ok := parseArticleRef(q); ok {
		return false
	}
	if rq := readQuery(q); rq.lawCite() || rq.rulingCite() {
		return false
	}
	words := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(".,،;:!?؟()«»\"'", r)
	})
	if len(words) < 2 {
		return false
	}
	if strings.ContainsAny(q, "?؟") {
		return true
	}
	for i, w := range words {
		w = strings.TrimPrefix(w, "و") // "وهل", "وكيف"
		if questionWords[w] || questionWords[words[i]] {
			return true
		}
		// "what is", "can i", "est-ce que": only at the start, where they ask.
		if i == 0 && questionStarts[w] {
			return true
		}
	}
	// A long search with no question word is still a question unless it reads
	// as the title of a text ("قانون ...", "مرسوم ...").
	if len(words) >= 7 && !titleStarts[strings.TrimPrefix(words[0], "ال")] && !titleStarts[words[0]] {
		return true
	}
	return false
}

// Words that ask wherever they stand.
var questionWords = setOf(
	// Arabic
	"هل", "ماذا", "لماذا", "كيف", "متى", "أين", "اين", "كم", "ماهي", "ماهو", "يحق", "يجوز", "يمكنني", "يمكن", "أستطيع", "استطيع",
	// Lebanese Arabic
	"شو", "كيف", "ليش", "قديش", "وين", "فيني", "بقدر", "بيحق", "بيحقلي", "منين",
	// Arabizi
	"shu", "chou", "kif", "adesh", "2adesh", "fini", "ba2dar", "leh", "lesh",
	// English, French
	"why", "how", "whether", "pourquoi", "comment", "combien", "quand",
)

// Words that ask only when the search starts with them.
var questionStarts = setOf(
	"ما", "من", "اذا", "إذا", "لو",
	"what", "when", "who", "which", "where", "can", "could", "is", "are", "do", "does", "did", "should", "may", "must", "if", "am",
	"quel", "quelle", "quels", "quelles", "est-ce", "peut-on", "puis-je", "qui", "que", "si",
)

// First words of text titles: a long search starting with one is a title.
var titleStarts = setOf(
	"قانون", "مرسوم", "قرار", "نظام", "تعميم", "اتفاقية", "معاهدة", "تعديل", "اجازة", "إجازة", "الغاء", "إلغاء", "تنظيم", "انشاء", "إنشاء", "تحديد", "تصديق", "ابرام", "إبرام",
	"law", "decree", "decision", "loi", "décret", "arrêté", "code",
)

func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}
