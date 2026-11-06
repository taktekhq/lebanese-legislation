package mcpserver

import (
	"regexp"
	"strings"
)

// arNorm is Postgres's ar_norm (db/schema.sql): it drops tashkeel and
// tatweel and folds أ إ آ ٱ to ا, ى to ي, ة to ه, so one spelling of a word
// finds every other. Queries are normalized with it before they are matched
// against ar_norm'd text.
func arNorm(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case (r >= 0x064B && r <= 0x065F) || r == 0x0670 || r == 0x0640:
			continue
		case r == 'أ' || r == 'إ' || r == 'آ' || r == 'ٱ':
			b.WriteRune('ا')
		case r == 'ى':
			b.WriteRune('ي')
		case r == 'ة':
			b.WriteRune('ه')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// arStop holds the little words that no search should be held up by, after arNorm.
var arStop = setOf("في", "من", "على", "عن", "الى", "او", "ان", "التي", "الذي", "هل", "ما", "لدى", "رقم", "تاريخ", "بتاريخ", "و")

var (
	stemPrefixes = []string{"وال", "بال", "كال", "فال", "لل", "ال"}
	stemSuffixes = []string{"ات", "ين", "ون", "ه"}
)

// lightStem is a word (after arNorm) less a leading article ("ال", "وال",
// "بال", "لل"...) and a feminine or plural ending, when three letters are
// left: "الصوريه" is "صوري", "المحاكمات" is "محاكم", "والعقود" is "عقود".
func lightStem(w string) string {
	s := w
	for _, p := range stemPrefixes {
		if strings.HasPrefix(s, p) && runeLen(s)-runeLen(p) >= 3 {
			s = s[len(p):]
			break
		}
	}
	for _, x := range stemSuffixes {
		if strings.HasSuffix(s, x) && runeLen(s)-runeLen(x) >= 3 {
			s = s[:len(s)-len(x)]
			break
		}
	}
	return s
}

var (
	edgePunct  = regexp.MustCompile(`^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$`)
	arabicChar = regexp.MustCompile(`[\x{0600}-\x{06FF}]`)
)

// trimEdges drops anything but letters and digits from both ends of a word.
func trimEdges(w string) string { return edgePunct.ReplaceAllString(w, "") }

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}
